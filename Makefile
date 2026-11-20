.PHONY: all build run dev test vet clean docker-build docker-push docker-up docker-down

BINARY_NAME=argus
TAG ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "latest")
IMAGE_NAME ?= winajr/argus
DOCKER_IMAGE ?= $(IMAGE_NAME):$(TAG)

all: vet test build

build:
	go build -trimpath -ldflags="-s -w -X main.version=$(TAG)" -o bin/$(BINARY_NAME) ./cmd/argus

run:
	go run ./cmd/argus -config config.example.yaml

dev:
	air

test:
	go test -v -race -count=1 ./...

vet:
	go vet ./...

clean:
	rm -rf bin/ tmp/

docker-build:
	docker build --build-arg VERSION=$(TAG) -t $(DOCKER_IMAGE) -t $(IMAGE_NAME):latest .

docker-push: docker-build
	docker push $(DOCKER_IMAGE)
	@if [ "$(TAG)" != "latest" ]; then docker push $(IMAGE_NAME):latest; fi

docker-up:
	docker compose up -d

docker-down:
	docker compose down
