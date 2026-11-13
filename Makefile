.PHONY: all build run dev test vet lint clean

BINARY_NAME=argus

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
