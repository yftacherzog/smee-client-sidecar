# Stage 1: Build the Go binary
FROM registry.access.redhat.com/ubi9/go-toolset:9.6-1747333074 AS builder
ARG TARGETOS
ARG TARGETARCH

ENV GOTOOLCHAIN=auto
WORKDIR /workspace

# Copy go.mod and go.sum files to download dependencies
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as
# much and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the rest of the source code
COPY cmd/main.go cmd/main.go

# Build the binary with flags for a small, static executable
# RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /smee-client-sidecar .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o /opt/app-root/smee-client-sidecar cmd/main.go

# Stage 2: Create the final, minimal image
# FROM scratch
FROM registry.access.redhat.com/ubi9/ubi:9.5-1744101466

# Copy the static binary from the builder stage
WORKDIR /
COPY --from=builder /opt/app-root/smee-client-sidecar .
COPY LICENSE /licenses/
# TODO: figure out proper ca-trust solution
# USER 0
# ADD --chown=root:root --chmod=644 data/ca-trust/* /etc/pki/ca-trust/source/anchors
# RUN /usr/bin/update-ca-trust
USER 65532:65532

# It is mandatory to set these labels
LABEL name="Smee Client Instrumentation Sidecar"
LABEL description="Smee Client Instrumentation Sidecar"
LABEL com.redhat.component="Smee Client Instrumentation Sidecar"
LABEL io.k8s.description="Smee Client Instrumentation Sidecar"
LABEL io.k8s.display-name="smee-client-sidecar"

# Set the entrypoint for the container
ENTRYPOINT ["/smee-client-sidecar"]
