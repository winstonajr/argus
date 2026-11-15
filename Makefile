.PHONY: all build run dev test vet clean docker-build docker-push docker-up docker-down

BINARY_NAME=argus
TAG ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "latest")
IMAGE_NAME ?= winajr/argus
DOCKER_IMAGE ?= $(IMAGE_NAME):$(TAG)

all: test build

build:
	go build -o bin/$(BINARY_NAME) ./cmd/argus

run:
	go run ./cmd/argus -config config.example.yaml

dev:
	air

test:
	go test -v -race ./...

vet:
	go vet ./...

clean:
	rm -rf bin/ tmp/ data/

docker-build:
	docker build -t $(DOCKER_IMAGE) -t $(IMAGE_NAME):latest .

docker-push: docker-build
	docker push $(DOCKER_IMAGE)
	@if [ "$(TAG)" != "latest" ]; then docker push $(IMAGE_NAME):latest; fi

docker-up:
	docker compose up -d

docker-down:
	docker compose down
