# ── 项目配置 ──────────────────────────────────────────────
# 同一份源码编出两个版本，差异**只在构建标签**，不靠删文件或改分支：
#
#   full（bin/mocca）     REST API + passwd + 管理后台（含封面图制作）
#   api （bin/mocca-api） REST API + passwd。后台与其封面图制作被编译期剥离
#
# 标签的实现落点：web/embed.go（!noweb）与 web/noweb.go（noweb），
# 封面图接口同理（handlers/cover_admin.go 与 handlers/cover_api.go）。
APP     = mocca
APP_API = mocca-api
TAG_API = noweb      # 纯 API 版使用的构建标签

# 发布用目标平台。两种版本共用同一份清单，一次编齐。
PLATFORMS = linux/amd64 linux/arm64 darwin/arm64 darwin/amd64 windows/amd64

# ── 构建参数 ──────────────────────────────────────────────
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# 发布裁剪参数：
#   -s -w     去符号表与 DWARF 调试信息。实测纯后端二进制 33MB → 22MB（-33%）。
#             panic 栈里的函数名与行号来自 pclntab，不受这两项影响，运行时可读性不变。
#   -trimpath 去掉源码绝对路径，既避免泄露构建机目录，也让构建可复现。
# 需要 delve/gdb 单步调试时用 `make LDFLAGS= ...` 显式清空即可。
LDFLAGS  ?= -s -w
GOBUILD   = go build -trimpath -ldflags="$(LDFLAGS)"

# ── 目标 ──────────────────────────────────────────────────

.PHONY: all api web build-api build-full run run-api server clean test cover lint tidy

## api: 纯 API 版（本机平台）—— 无管理后台、无封面图制作
api:
	@echo "Build $(APP_API) (API only: no admin UI, no cover maker) ..."
	mkdir -p bin
	CGO_ENABLED=0 $(GOBUILD) -tags $(TAG_API) -o bin/$(APP_API) ./
	@echo "✅ $(APP_API) 已生成（含 passwd 子命令）"

## web: 完整版（本机平台）—— 带管理后台与封面图制作
web:
	@echo "Build $(APP) (web: API + admin UI + cover maker) ..."
	mkdir -p bin
	CGO_ENABLED=0 $(GOBUILD) -o bin/$(APP) ./
	@echo "✅ $(APP) 已生成（管理后台在 /admin/）"

## build-api: 交叉编译纯 API 版到 $(PLATFORMS)
build-api:
	@echo "Cross-compiling $(APP_API) (API only) for $(PLATFORMS) ..."
	mkdir -p bin
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		out="bin/$(APP_API)-$(VERSION).$$os-$$arch$$ext"; \
		echo "  → $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GOBUILD) -tags $(TAG_API) -o "$$out" ./ || exit 1; \
	done
	@echo "✅ 纯 API 版交叉编译完成"

## build-full: 交叉编译完整版到 $(PLATFORMS)
build-full:
	@echo "Cross-compiling $(APP) (full) for $(PLATFORMS) ..."
	mkdir -p bin
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		out="bin/$(APP)-$(VERSION).$$os-$$arch$$ext"; \
		echo "  → $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GOBUILD) -o "$$out" ./ || exit 1; \
	done
	@echo "✅ 完整版交叉编译完成"

## all: 交叉编译两个版本的全平台产物
all: clean api web build-api build-full

## run: 本机直接跑完整版（前端是普通 JS/CSS，随二进制内嵌，无需额外构建步骤）
run:
	go run ./

## run-api: 本机直接跑纯 API 版
run-api:
	go run -tags $(TAG_API) ./

## server: 编译完整版并启动
server: web
	./bin/$(APP)

## clean: 清理构建产物
clean:
	rm -rf bin/* tmp/*
	@echo "✅ Clean complete."

## test: 跑 Go 测试（race + 覆盖率聚合），可 TAGS=... 透传
test:
	go test -race -coverpkg=./... ./... -coverprofile=/tmp/mocca.cover $(TAGS)
	@go tool cover -func=/tmp/mocca.cover | tail -1

## test-api: 用纯 API 版的标签跑测试（验证剥离后仍能编译、行为符合预期）
test-api:
	go test -tags $(TAG_API) ./...

## cover: 生成覆盖率报告并在浏览器打开
cover:
	@go test -coverpkg=./... ./... -coverprofile=/tmp/mocca.cover > /dev/null && go tool cover -html=/tmp/mocca.cover

## lint: 对全项目跑 golangci-lint
lint:
	golangci-lint run ./...

## tidy: 整理 go.mod 依赖
tidy:
	go mod tidy
