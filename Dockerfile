FROM registry.svc.ci.openshift.org/openshift/release:golang-1.10 AS builder
WORKDIR /go/src/github.com/openshift/local-storage-operator
COPY . .
RUN make build

FROM registry.svc.ci.openshift.org/openshift/origin-v4.0:base
COPY --from=builder /go/src/github.com/openshift/local-storage-operator/local-storage-operator /usr/bin/
COPY manifests /manifests
ENTRYPOINT ["/usr/bin/local-storage-operator"]
LABEL io.openshift.release.operator true
LABEL io.k8s.display-name="OpenShift local-storage-operator" \
      io.k8s.description="This is a component of OpenShift and manages local volumes." \
        maintainer="Hemant Kumar <hekumar@redhat.com>"
