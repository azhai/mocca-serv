# ── 项目配置 ──────────────────────────────────────────────
# 同一份源码编出两个版本，差异**只在构建标签**，不靠删文件或改分支：
#
#   full（bin/mocca）     APP 接口 + 管理接口 + 内嵌网页（浏览应用与后台）
#   api （bin/mocca-api） APP 接口 + passwd。管理接口与网页被编译期剥离
#
# 标签的实现落点：
#   web/embed.go（!noweb）与 web/noweb.go（noweb）—— 网页资源的嵌入与否；
#   routes/build_full.go（!noweb）与 routes/build_api.go（noweb）—— AdminRoutes 常量；
#   goent 的 nopgsql（两版都带）—— 本服务只连 sqlite，不让 PostgreSQL 驱动进二进制。
#   落点在对端仓库 goent：drivers/dialect_pgsql.go 与 schema_ops_pgsql.go 各自有两个
#   变体（!nopgsql / nopgsql），把打开驱动与造 schema 驱动这两处换成空实现。
#
# API 版保留的接口（APP 用得到的）：初始化/注册/登录、me、收藏、评论、弹幕（含 SSE）、
# 列目录/取详情/取流、海报、目录密码状态、探活。
# API 版剥离的接口：存储维护、上传/改名/移动/删除、媒体编辑、封面/截图/HLS/重建索引/
# 补截图、TMDB 刮削、目录密码设置、全局设置、用户管理 —— 见 routes/admin.go。
APP     = mocca
APP_API = mocca-api
# 构建标签。定义里不写行尾注释 —— make 会把注释前的空白算进变量值，
# 而这两个值要用 `$(TAG_API),$(TAG_NOPG)` 逗号拼起来，留白就成了非法标签。
#   TAG_API  noweb    纯 API 版专用（剥离管理接口与网页）
#   TAG_NOPG nopgsql  两版都带（去掉 goent 的 PostgreSQL 驱动）
# 实测 nopgsql 连带去掉 pgx/puddle/x-text 共 18 个包：
# 完整版 19.0MB → 15.4MB（-3.6MB），API 版 17.4MB → 13.7MB（-3.7MB）。
TAG_API = noweb
TAG_NOPG = nopgsql

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

.PHONY: all api web full build-api build-full run run-api server clean test cover lint tidy

## api: 纯 API 版（本机平台）—— 只有 APP 用得到的接口，无管理接口、无网页
api:
	@echo "Build $(APP_API) (API only: app endpoints, no admin routes, no admin UI) ..."
	mkdir -p bin
	CGO_ENABLED=0 $(GOBUILD) -tags $(TAG_API),$(TAG_NOPG) -o bin/$(APP_API) ./
	@echo "✅ $(APP_API) 已生成（含 passwd 子命令）"

## web: 完整版（本机平台）—— APP 接口 + 管理接口 + 内嵌网页
web:
	@echo "Build $(APP) (full: app + admin routes + embedded UI) ..."
	mkdir -p bin
	CGO_ENABLED=0 $(GOBUILD) -tags $(TAG_NOPG) -o bin/$(APP) ./
	@echo "✅ $(APP) 已生成（管理后台在 /admin/）"

## full: web 的别名 —— 文档与脚本里一直写 `make full`，这里对齐
full: web

## build-api: 交叉编译纯 API 版到 $(PLATFORMS)
build-api:
	@echo "Cross-compiling $(APP_API) (API only) for $(PLATFORMS) ..."
	mkdir -p bin
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		out="bin/$(APP_API)-$(VERSION).$$os-$$arch$$ext"; \
		echo "  → $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GOBUILD) -tags $(TAG_API),$(TAG_NOPG) -o "$$out" ./ || exit 1; \
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
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GOBUILD) -tags $(TAG_NOPG) -o "$$out" ./ || exit 1; \
	done
	@echo "✅ 完整版交叉编译完成"

## all: 交叉编译两个版本的全平台产物
all: clean api web build-api build-full

## run: 本机直接跑完整版（前端是普通 JS/CSS，随二进制内嵌，无需额外构建步骤）
run:
	go run -tags $(TAG_NOPG) ./

## run-api: 本机直接跑纯 API 版
run-api:
	go run -tags $(TAG_API),$(TAG_NOPG) ./

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

## test-api: 用纯 API 版的标签跑测试。
## 既验证剥离后仍能编译，也验证**剥离确实发生**：routes 包的路由表测试
## 会断言管理接口在这一构建里没有注册（见 routes/routes_test.go）。
## 标签与发布一致（noweb,nopgsql），所以这一趟同时覆盖 nopgsql 组合的编译。
test-api:
	go test -tags $(TAG_API),$(TAG_NOPG) ./...

## cover: 生成覆盖率报告并在浏览器打开
cover:
	@go test -coverpkg=./... ./... -coverprofile=/tmp/mocca.cover > /dev/null && go tool cover -html=/tmp/mocca.cover

## lint: 对全项目跑 golangci-lint
lint:
	golangci-lint run ./...

## tidy: 整理 go.mod 依赖
tidy:
	go mod tidy
