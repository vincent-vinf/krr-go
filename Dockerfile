# Build the manager binary
FROM --platform=$BUILDPLATFORM  golang:1.22 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
COPY vendor/ vendor/

# Copy the go source
COPY cmd/ cmd/
COPY pkg/ pkg/

RUN echo "Target platform: $TARGETPLATFORM" && \
    export PLATFORM="${TARGETPLATFORM}" && \
    export GOOS=$(echo "${PLATFORM}" | cut -d / -f2) && \
    export GOARCH=$(echo "${PLATFORM}" | cut -d / -f1) && \
    echo "GOOS set to: $GOOS" && \
    echo "GOARCH set to: $GOARCH"

# Build
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH \
    go build -a -o main cmd/main/main.go

FROM --platform=$TARGETPLATFORM alpine
WORKDIR /krr
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apk/repositories && \
    apk add --no-cache tzdata

COPY --from=builder /workspace/main /usr/local/bin/krr
#USER root:root

