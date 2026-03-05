.PHONY: build build-linux

BINARY_NAME=mockllm
MAIN_PACKAGE=./cmd/server

# 本地编译
build:
	go build -o $(BINARY_NAME) $(MAIN_PACKAGE)

# Linux AMD64 交叉编译
build-linux:
	GOOS=linux GOARCH=amd64 go build -o $(BINARY_NAME)-linux-amd64 $(MAIN_PACKAGE)
