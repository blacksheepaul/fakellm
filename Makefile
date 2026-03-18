.PHONY: build-osx build-linux run

run: build-linux
	docker compose down
	docker compose up -d --build

build-osx:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -trimpath -o bin/fakellm-darwin-arm64 ./cmd/server
	tar -czf bin/fakellm-darwin-arm64.tar.gz -C bin fakellm-darwin-arm64

build-linux:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -trimpath -o bin/fakellm-linux-amd64 ./cmd/server
	tar -czf bin/fakellm-linux-amd64.tar.gz -C bin fakellm-linux-amd64
	
