.PHONY: build build-linux

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o bin/fakellm ./cmd/server

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -trimpath -o bin/fakellm-linux-amd64 ./cmd/server
