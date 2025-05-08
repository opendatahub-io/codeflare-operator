# Build the manager binary

# BEGIN -- workaround lack of go-toolset for golang 1.23
ARG GOLANG_IMAGE=docker.io/library/golang:1.23
FROM ${GOLANG_IMAGE} AS golang

FROM registry.access.redhat.com/ubi8/ubi@sha256:3e6410626323e6f11af16b28b8e73faa4799fe0dbdb4b1cfbf6f029c34b038f3 AS builder
ARG GOLANG_VERSION=1.23.0

# Install system dependencies
RUN dnf upgrade -y && dnf install -y \
    gcc \
    make \
    openssl-devel \
    git \
    && dnf clean all && rm -rf /var/cache/yum

# Install Go
ENV PATH=/usr/local/go/bin:$PATH

COPY --from=golang /usr/local/go /usr/local/go
# End of Go versioning workaround

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
COPY ./Makefile ./Makefile
RUN go mod download

# Copy the Go sources
COPY main.go main.go
COPY pkg/ pkg/

# Build
USER root
RUN CGO_ENABLED=1 GOOS=linux GOARCH=${GOARCH} make go-build-for-image

FROM registry.access.redhat.com/ubi8/ubi-minimal:8.8
WORKDIR /
COPY --from=builder /workspace/manager .

USER 65532:65532
ENTRYPOINT ["/manager"]
