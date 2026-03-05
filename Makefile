.PHONY: build build-linux build-small build-linux-small clean

BINARY_NAME=fakellm
OUTPUT_DIR=bin
MAIN_PACKAGE=./cmd/server

# 优化标志说明：
# CGO_ENABLED=0: 禁用 CGO，生成静态链接二进制
# -ldflags="-s -w": strip 符号表和 DWARF 调试信息
# -trimpath: 移除编译路径信息
LDFLAGS=-ldflags="-s -w"
BUILD_FLAGS=-trimpath

# 本地编译（优化版，推荐）
build:
	mkdir -p $(OUTPUT_DIR)
	CGO_ENABLED=0 go build $(LDFLAGS) $(BUILD_FLAGS) -o $(OUTPUT_DIR)/$(BINARY_NAME) $(MAIN_PACKAGE)

# Linux AMD64 交叉编译（优化版，推荐）
build-linux:
	mkdir -p $(OUTPUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) $(BUILD_FLAGS) -o $(OUTPUT_DIR)/$(BINARY_NAME)-linux-amd64 $(MAIN_PACKAGE)

# 使用 upx 进一步压缩（需要系统安装 upx）
build-small: build
	upx --best --lzma $(OUTPUT_DIR)/$(BINARY_NAME) 2>/dev/null || echo "upx not installed, skipping compression"

build-linux-small: build-linux
	upx --best --lzma $(OUTPUT_DIR)/$(BINARY_NAME)-linux-amd64 2>/dev/null || echo "upx not installed, skipping compression"

# 显示对比：优化前后的大小
clean:
	rm -rf $(OUTPUT_DIR)
