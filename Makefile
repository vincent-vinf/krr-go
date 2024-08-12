GIT_COMMIT := $(shell git rev-parse --short HEAD)
IMG ?= registry.cn-hangzhou.aliyuncs.com/adpc/krr-go:$(GIT_COMMIT)

.PHONY: build-push
build-push: ## Build docker image with the manager.
	go mod vendor
	docker buildx build --platform=linux/amd64,linux/arm64 -t ${IMG} -o type=registry .

