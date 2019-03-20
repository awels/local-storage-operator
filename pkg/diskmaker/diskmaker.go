package diskmaker

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
)

var (
	checkDuration = 5 * time.Second
	diskByIDPath  = "/dev/disk/by-id/*"
)

type DiskMaker struct {
	configLocation  string
	symlinkLocation string
}

type DiskLocation struct {
	diskName string
	diskID   string
}

// DiskMaker returns a new instance of DiskMaker
func NewDiskMaker(configLocation, symLinkLocation string) *DiskMaker {
	t := &DiskMaker{}
	t.configLocation = configLocation
	t.symlinkLocation = symLinkLocation
	return t
}

func (d *DiskMaker) loadConfig() (DiskConfig, error) {
	var err error
	content, err := ioutil.ReadFile(d.configLocation)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s with %v", d.configLocation, err)
	}
	var diskConfig DiskConfig
	err = yaml.Unmarshal(content, &diskConfig)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling %s with %v", d.configLocation, err)
	}
	return diskConfig, nil
}

// Run and create disk config
func (d *DiskMaker) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(checkDuration)
	defer ticker.Stop()

	err := os.MkdirAll(d.symlinkLocation, 0755)
	if err != nil {
		logrus.Errorf("error creating local-storage directory %s with %v", d.symlinkLocation, err)
		os.Exit(-1)
	}

	for {
		select {
		case <-ticker.C:
			diskConfig, err := d.loadConfig()
			if err != nil {
				logrus.Errorf("error loading configuration with %v", err)
				break
			}
			d.symLinkDisks(diskConfig)
		case <-stop:
			logrus.Infof("exiting, received message on stop channel")
			os.Exit(0)
		}
	}
}

func (d *DiskMaker) symLinkDisks(diskConfig DiskConfig) {
	cmd := exec.Command("lsblk", "--list", "-o", "NAME,MOUNTPOINT", "--noheadings")
	var out bytes.Buffer
	var err error
	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		logrus.Errorf("error running lsblk %v", err)
		return
	}
	deviceSet, err := d.findNewDisks(out.String())
	if err != nil {
		logrus.Errorf("error unmrashalling json %v", err)
		return
	}

	if len(deviceSet) == 0 {
		logrus.Infof("unable to find any new disks")
		return
	}

	deviceMap, err := d.findMatchingDisks(diskConfig, deviceSet)
	if err != nil {
		logrus.Errorf("error matching finding disks : %v", err)
		return
	}

	if len(deviceMap) == 0 {
		logrus.Errorf("unable to find any matching disks")
		return
	}

	for storageClass, deviceArray := range deviceMap {
		for _, deviceNameLoction := range deviceArray {
			symLinkDirPath := path.Join(d.symlinkLocation, storageClass)
			err := os.MkdirAll(symLinkDirPath, 0755)
			if err != nil {
				logrus.Errorf("error creating symlink directory %s with %v", symLinkDirPath, err)
				continue
			}
			symLinkPath := path.Join(symLinkDirPath, deviceNameLoction.diskName)
			logrus.Infof("symlinking to %s to %s", deviceNameLoction.diskID, symLinkPath)
			symLinkErr := os.Symlink(deviceNameLoction.diskID, symLinkPath)
			if symLinkErr != nil {
				logrus.Errorf("error creating symlink %s with %v", symLinkPath, err)
			}
		}
	}

}

func (d *DiskMaker) findMatchingDisks(diskConfig DiskConfig, deviceSet sets.String) (map[string][]DiskLocation, error) {
	blockDeviceMap := make(map[string][]DiskLocation)
	addDiskToMap := func(scName, stableDeviceID, diskName string) {
		deviceArray, ok := blockDeviceMap[scName]
		if !ok {
			deviceArray = []DiskLocation{}
		}
		deviceArray = append(deviceArray, DiskLocation{diskName, stableDeviceID})
		blockDeviceMap[scName] = deviceArray
	}
	for storageClass, disks := range diskConfig {
		// handle diskNames
		for _, diskName := range disks.DiskNames {
			if hasExactDisk(deviceSet, diskName) {
				devicePath, err := d.findStableDeviceID(diskName)
				if err != nil {
					logrus.Errorf("Unable to add disk %s to local disk pool %v", diskName, err)
					continue
				}
				addDiskToMap(storageClass, devicePath, diskName)
				continue
			}
		}
		// handle DeviceIDs
		for _, deviceID := range disks.DeviceIDs {
			diskPath, deviceName, err := d.findDeviceByID(deviceID)
			if err != nil {
				logrus.Errorf("unable to add disk %s to local disk pool %v", deviceID, err)
				continue
			}
			addDiskToMap(storageClass, diskPath, deviceName)
		}
	}
	return blockDeviceMap, nil
}

// findDeviceByID finds device ID and return name and path
func (d *DiskMaker) findDeviceByID(deviceID string) (string, string, error) {
	allDisks, err := filepath.Glob(diskByIDPath)
	if err != nil {
		return "", "", fmt.Errorf("error finding disk %s with %v", deviceID, err)
	}
	for _, disk := range allDisks {
		drive, err := filepath.EvalSymlinks(disk)
		if err != nil {
			continue
		}
		diskNameParts := strings.Split(drive, "/")
		realDiskName := strings.TrimSpace(diskNameParts[len(diskNameParts)-1])

		diskIDParts := strings.Split(disk, "/")
		realDiskID := strings.TrimSpace(diskIDParts[len(diskIDParts)-1])
		if realDiskID == deviceID {
			return disk, realDiskName, nil
		}

	}
	return "", "", fmt.Errorf("unable to find ID of disk %s", deviceID)
}

func (d *DiskMaker) findStableDeviceID(diskName string) (string, error) {
	allDisks, err := filepath.Glob(diskByIDPath)
	if err != nil {
		return "", fmt.Errorf("error finding disk %s with %v", diskName, err)
	}
	for _, disk := range allDisks {
		drive, err := filepath.EvalSymlinks(disk)
		if err != nil {
			continue
		}
		diskNameParts := strings.Split(drive, "/")
		realDiskName := strings.TrimSpace(diskNameParts[len(diskNameParts)-1])
		if realDiskName == diskName {
			return disk, nil
		}
	}
	return "", fmt.Errorf("unable to find ID of disk %s", diskName)
}

func (d *DiskMaker) findNewDisks(content string) (sets.String, error) {
	deviceSet := sets.NewString()
	deviceLines := strings.Split(content, "\n")
	for _, deviceLine := range deviceLines {
		deviceLine := strings.TrimSpace(deviceLine)
		deviceDetails := strings.Split(deviceLine, " ")
		// We only consider devices that are not mounted.
		// TODO: We should also consider checking for device partitions, so as
		// if a device has partitions then we do not consider the device. We only
		// consider partitions.
		if len(deviceDetails) == 1 && len(deviceDetails[0]) > 0 {
			deviceSet.Insert(deviceDetails[0])
		}
	}
	return deviceSet, nil
}

func hasExactDisk(disks sets.String, device string) bool {
	for _, disk := range disks.List() {
		if disk == device {
			return true
		}
	}
	return false
}
