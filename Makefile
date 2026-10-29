.PHONY: all build run test vet lint clean

BINARY_NAME=sentinela

all: test build

build:
	go build -o bin/$(BINARY_NAME) ./cmd/sentinela

run:
	go run ./cmd/sentinela -config config.example.yaml

test:
	go test -v -race ./...

vet:
	go vet ./...

clean:
	rm -rf bin/ data/
