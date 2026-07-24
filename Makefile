GO ?= go
DOCKER ?= docker
IMAGE ?= save-sync-ps-pc:latest
BIN_DIR ?= bin
DIST_DIR ?= dist
VERSION ?= dev
LDFLAGS ?= -s -w -X main.version=$(VERSION)
DOCKER_GO_IMAGE ?= golang:1.22-bookworm
DOCKER_USER ?= $(shell id -u):$(shell id -g)
UI_HOST ?= 127.0.0.1
UI_PORT ?= 8765

all: fmt test build release

help:
	@printf '%s\n' \
		'Targets:' \
		'  make all              Format, test, and build Go binaries' \
		'  make fmt              Run gofmt' \
		'  make test             Run Go tests' \
		'  make build            Build native host binaries into bin/' \
		'  make release          Build Linux and Windows binaries into dist/' \
		'  make linux            Build Linux amd64 and arm64 binaries' \
		'  make windows          Build Windows amd64 binaries' \
		'  make ui               Run local browser UI service' \
		'  make bridge-help      Show save-sync help' \
		'  make docker-build     Build PC bridge Docker image' \
		'  make docker-bin       Export Linux binaries from Docker image to dist/docker/' \
		'  make docker-release   Build Linux/Windows binaries in Docker into dist/' \
		'  make docker-help      Show bridge help inside Docker image' \
		'  make docker-ui        Run browser UI inside Docker image' \
		'  make clean            Remove build/cache artifacts'

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/save-sync ./cmd/save-sync
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/save-sync-ui ./cmd/save-sync-ui

release:
	sh scripts/build-release.sh

linux:
	mkdir -p $(DIST_DIR)/linux-amd64 $(DIST_DIR)/linux-arm64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/linux-amd64/save-sync ./cmd/save-sync
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/linux-amd64/save-sync-ui ./cmd/save-sync-ui
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/linux-arm64/save-sync ./cmd/save-sync
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/linux-arm64/save-sync-ui ./cmd/save-sync-ui

windows:
	mkdir -p $(DIST_DIR)/windows-amd64
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/windows-amd64/save-sync.exe ./cmd/save-sync
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/windows-amd64/save-sync-ui.exe ./cmd/save-sync-ui

bridge-help:
	$(GO) run ./cmd/save-sync --help

ui:
	$(GO) run ./cmd/save-sync-ui --host $(UI_HOST) --port $(UI_PORT)

docker-build:
	$(DOCKER) build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

docker-bin: docker-build
	mkdir -p $(DIST_DIR)/docker
	-$(DOCKER) rm -f save-sync-bin-tmp
	$(DOCKER) create --name save-sync-bin-tmp $(IMAGE)
	$(DOCKER) cp save-sync-bin-tmp:/usr/local/bin/save-sync $(DIST_DIR)/docker/save-sync
	$(DOCKER) cp save-sync-bin-tmp:/usr/local/bin/save-sync-ui $(DIST_DIR)/docker/save-sync-ui
	rm -rf $(DIST_DIR)/docker/games
	$(DOCKER) cp save-sync-bin-tmp:/app/games $(DIST_DIR)/docker/games
	$(DOCKER) rm save-sync-bin-tmp

docker-release:
	$(DOCKER) build --build-arg VERSION=$(VERSION) --target release -t $(IMAGE)-release .
	-$(DOCKER) rm -f save-sync-release-tmp
	$(DOCKER) create --name save-sync-release-tmp $(IMAGE)-release
	rm -rf $(DIST_DIR)
	$(DOCKER) cp save-sync-release-tmp:/dist $(DIST_DIR)
	$(DOCKER) rm save-sync-release-tmp

docker-help: docker-build
	$(DOCKER) run --rm $(IMAGE) save-sync --help

docker-ui: docker-build
	$(DOCKER) run --rm -p $(UI_PORT):8765 $(IMAGE) save-sync-ui --host 0.0.0.0 --port 8765

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR)

.PHONY: all help fmt test build release linux windows bridge-help ui docker-build docker-bin docker-release docker-help docker-ui clean
