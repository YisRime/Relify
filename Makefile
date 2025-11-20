# Relify Makefile - 跨平台构建

PROJECT_NAME := relify
MAIN_PATH := ./cmd/relify
OUTPUT_DIR := ./bin

VERSION := $(shell git describe --tags --always 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date +%Y-%m-%d_%H:%M:%S)
LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)

.PHONY: all clean windows linux windows-amd64 windows-arm64 linux-amd64 linux-arm64

all: windows linux

# Windows 构建
windows: windows-amd64 windows-arm64

windows-amd64:
	@echo "构建 Windows amd64..."
	@mkdir -p $(OUTPUT_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o $(OUTPUT_DIR)/$(PROJECT_NAME)-windows-amd64.exe -ldflags "$(LDFLAGS)" $(MAIN_PATH)
	@echo "✓ Windows amd64 构建完成"

windows-arm64:
	@echo "构建 Windows arm64..."
	@mkdir -p $(OUTPUT_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -o $(OUTPUT_DIR)/$(PROJECT_NAME)-windows-arm64.exe -ldflags "$(LDFLAGS)" $(MAIN_PATH)
	@echo "✓ Windows arm64 构建完成"

# Linux 构建
linux: linux-amd64 linux-arm64

linux-amd64:
	@echo "构建 Linux amd64..."
	@mkdir -p $(OUTPUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(OUTPUT_DIR)/$(PROJECT_NAME)-linux-amd64 -ldflags "$(LDFLAGS)" $(MAIN_PATH)
	@echo "✓ Linux amd64 构建完成"

linux-arm64:
	@echo "构建 Linux arm64..."
	@mkdir -p $(OUTPUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o $(OUTPUT_DIR)/$(PROJECT_NAME)-linux-arm64 -ldflags "$(LDFLAGS)" $(MAIN_PATH)
	@echo "✓ Linux arm64 构建完成"

# 清理构建产物
clean:
	@echo "清理构建产物..."
	@rm -rf $(OUTPUT_DIR)
	@echo "✓ 清理完成"

# 显示帮助信息
help:
	@echo "Relify 构建系统"
	@echo ""
	@echo "使用方法:"
	@echo "  make all           - 构建所有平台版本"
	@echo "  make windows       - 构建所有 Windows 版本"
	@echo "  make linux         - 构建所有 Linux 版本"
	@echo "  make windows-amd64 - 仅构建 Windows amd64"
	@echo "  make windows-arm64 - 仅构建 Windows arm64"
	@echo "  make linux-amd64   - 仅构建 Linux amd64"
	@echo "  make linux-arm64   - 仅构建 Linux arm64"
	@echo "  make clean         - 清理构建产物"
