.PHONY: docker-push

VERSION            ?= $(shell git rev-parse HEAD)
REGISTRY           ?= registry-write.opensource.zalan.do/teapot
IMAGE              ?= $(REGISTRY)/skipper:$(VERSION)
ARM64_IMAGE        ?= $(REGISTRY)/skipper-arm64:$(VERSION)
ARM_IMAGE          ?= $(REGISTRY)/skipper-armv7:$(VERSION)
ALPINE_BUILD_IMAGE ?= $(REGISTRY)/skipper-alpine-build:latest
PACKAGE            ?= github.com/zalando/skipper
CGO_ENABLED        ?= 0
GOOS               ?= linux
GOARCH             ?= amd64
GOARM              ?=
GO111              ?= on
COMMIT_HASH        = $(shell git rev-parse --short HEAD)

default: docker-build

skipper:
	GO111MODULE=$(GO111) \
	GOOS=$(GOOS) \
	GOARCH=$(GOARCH) \
	$(GOARM) \
	CGO_ENABLED=$(CGO_ENABLED) \
	go build -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH)" -o skipper ../cmd/skipper/*.go

eskip:
	GO111MODULE=$(GO111) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GOARM) CGO_ENABLED=$(CGO_ENABLED) go build -o eskip ../cmd/eskip/*.go

webhook:
	GO111MODULE=$(GO111) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GOARM) CGO_ENABLED=$(CGO_ENABLED) go build -o webhook ../cmd/webhook/*.go

routesrv:
	GO111MODULE=$(GO111) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GOARM) CGO_ENABLED=$(CGO_ENABLED) go build -o routesrv ../cmd/routesrv/*.go

clean:
	rm -rf skipper eskip webhook routesrv build/

docker-build: docker.build.amd64 docker.build.arm64 docker.build.armv7
docker.build.amd64: clean skipper eskip webhook routesrv
	docker build -t $(IMAGE) .
docker.build.arm64: clean build.linux.arm64 docker.build.enable
	docker buildx build -t $(ARM64_IMAGE) --platform linux/arm64 -f Dockerfile.arm64 .
docker.build.armv7: clean build.linux.armv7 docker.build.enable
	docker buildx build -t $(ARM_IMAGE) --platform linux/arm/v7 -f Dockerfile.arm .

docker-push: docker.push.amd64 docker.push.arm64 docker.push.armv7
docker.push.amd64:
	docker push $(IMAGE)
docker.push.arm64: docker.build.arm64
	docker push $(ARM64_IMAGE)
docker.push.armv7: docker.build.armv7
	docker push $(ARM_IMAGE)

# https://docs.docker.com/build/working-with-build/
# ~/.docker/config.json add: "experimental": "enabled",
docker.build.enable:
	[ $$(which jq) ] || echo "please install jq"
	[ -d $$HOME/.docker ] || mkdir $$HOME/.docker
	[ -f $$HOME/.docker/config.json ] || touch $$HOME/.docker/config.json
	if [ -s $$HOME/.docker/config.json ]; then jq -r '. += {experimental: "enabled"}' $$HOME/.docker/config.json > $$HOME/.docker/config.json.new; mv $$HOME/.docker/config.json.new $$HOME/.docker/config.json; else echo '{"experimental": "enabled"}' >$$HOME/.docker/config.json; fi

build.linux: $(SOURCES) build.linux.amd64 build.linux.arm64 build.linux.armv7

build.linux.amd64:
	GO111MODULE=$(GO111) \
	GOOS=linux \
	GOARCH=amd64 \
	CGO_ENABLED=$(CGO_ENABLED) \
	go build -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH)" -o build/linux/skipper ../cmd/skipper/*.go

build.linux.arm64:
	GO111MODULE=$(GO111) \
	GOOS=linux \
	GOARCH=arm64 \
	CGO_ENABLED=$(CGO_ENABLED) \
	go build -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH)" -o build/linux_arm8/skipper ../cmd/skipper/*.go

build.linux.armv7:
	GO111MODULE=$(GO111) \
	GOOS=linux \
	GOARCH=arm \
	GOARM=7 \
	CGO_ENABLED=$(CGO_ENABLED) \
	go build -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH)" -o build/linux_arm7/skipper ../cmd/skipper/*.go

build.osx:
	GO111MODULE=$(GO111) \
	GOOS=darwin \
	GOARCH=amd64 \
	CGO_ENABLED=$(CGO_ENABLED) \
	go build -o build/osx/skipper -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH)" ../cmd/skipper

build.windows:
	GO111MODULE=$(GO111) \
	GOOS=windows \
	GOARCH=amd64 \
	CGO_ENABLED=$(CGO_ENABLED) \
	go build -o build/windows/skipper.exe -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH)" ../cmd/skipper

build.package: build.linux build.osx build.windows
	tar --transform 's,^\.,skipper-$(VERSION)-linux-amd64,' -C build/linux -czvf skipper-$(VERSION)-linux-amd64.tar.gz .
	tar --transform 's,^\.,skipper-$(VERSION)-linux-arm8,' -C build/linux_arm8 -czvf skipper-$(VERSION)-linux-arm8.tar.gz .
	tar --transform 's,^\.,skipper-$(VERSION)-linux-arm7,' -C build/linux_arm7 -czvf skipper-$(VERSION)-linux-arm7.tar.gz .
	tar --transform 's,^\.,skipper-$(VERSION)-osx-amd64,' -C build/osx -czvf skipper-$(VERSION)-osx-amd64.tar.gz .
	tar --transform 's,^\.,skipper-$(VERSION)-windows-amd64,' -C build/windows -czvf skipper-$(VERSION)-windows-amd64.tar.gz .
	for f in *.tar.gz; do sha256sum $$f >> sha256sum.txt; done
