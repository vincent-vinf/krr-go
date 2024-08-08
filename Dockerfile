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

# Build
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -a -o main cmd/main/main.go

FROM --platform=$TARGETPLATFORM alpine
WORKDIR /krr
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apk/repositories && \
    apk add --no-cache tzdata

COPY --from=builder /workspace/main /usr/local/bin/krr
#USER root:root

