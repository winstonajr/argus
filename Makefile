.PHONY: all build run dev test vet clean docker-build docker-push docker-up docker-down

BINARY_NAME=argus
DOCKER_IMAGE=winajr/argus:latest

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
	docker build -t $(DOCKER_IMAGE) .

docker-push: docker-build
	docker push $(DOCKER_IMAGE)

docker-up:
	docker compose up -d

docker-down:
	docker compose down
