# fishtty Makefile
#
# 常用目标：
#   make all          — 构建全部（web + server + agent）
#   make build        — 同上
#   make build-server — 构建 server（含内嵌 PWA）
#   make build-agent  — 构建 agent
#   make build-web    — 构建前端 PWA
#   make proto        — 生成 Protobuf/Connect 代码
#   make test         — 运行全部测试
#   make docker       — 构建 Docker 镜像
#   make run-server   — 启动开发 Server
#   make clean        — 清理构建产物

.PHONY: all build build-server build-agent build-web proto test docker run-server run-agent clean lint

# 版本号（可通过 git tag 或 CI 注入）
VERSION ?= dev
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Go 构建标志
GO_LDFLAGS = -s -w -X main.Version=$(VERSION)

# ── 全部构建 ──

all: build

build: build-web build-server build-agent

# ── Protobuf 代码生成 ──

proto:
	buf lint
	buf generate
	@echo "✅ Protobuf 代码已生成"

# ── 前端构建 ──

build-web:
	cd web && pnpm install --frozen-lockfile && pnpm run build
	@echo "✅ Web PWA 已构建 (web/dist/)"

# ── Go 后端构建 ──

build-server: build-web
	go build -ldflags "$(GO_LDFLAGS)" -o bin/fishtty-server ./cmd/server/
	@echo "✅ fishtty-server 已构建 (bin/fishtty-server)"

build-agent:
	go build -ldflags "$(GO_LDFLAGS)" -o bin/fishtty-agent ./cmd/agent/
	@echo "✅ fishtty-agent 已构建 (bin/fishtty-agent)"

# ── 跨平台编译 Agent ──

build-agent-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(GO_LDFLAGS)" -o bin/fishtty-agent-linux-amd64 ./cmd/agent/
	@echo "✅ fishtty-agent (linux/amd64) 已构建 (bin/fishtty-agent-linux-amd64)"

build-agent-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(GO_LDFLAGS)" -o bin/fishtty-agent-linux-arm64 ./cmd/agent/
	@echo "✅ fishtty-agent (linux/arm64) 已构建 (bin/fishtty-agent-linux-arm64)"

build-agent-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(GO_LDFLAGS)" -o bin/fishtty-agent-darwin-arm64 ./cmd/agent/
	@echo "✅ fishtty-agent (darwin/arm64) 已构建 (bin/fishtty-agent-darwin-arm64)"

build-agent-all: build-agent-linux-amd64 build-agent-linux-arm64 build-agent-darwin-arm64
	@echo "✅ 全平台 Agent 构建完成"

# ── 测试 ──

test:
	go test ./internal/... -v -count=1 -timeout 30s
	go test ./test/... -v -count=1 -timeout 30s
	@echo "✅ 全部测试通过"

test-short:
	go test ./internal/... -count=1 -timeout 10s
	@echo "✅ 内部测试通过"

# ── 代码检查 ──

lint:
	buf lint
	go vet ./...
	cd web && pnpm exec tsc --noEmit
	@echo "✅ 代码检查通过"

# ── Docker ──

docker:
	docker build -t fishtty-server:$(VERSION) .
	@echo "✅ Docker 镜像已构建: fishtty-server:$(VERSION)"

# ── 本地运行（开发） ──

run-server:
	go run ./cmd/server/ --listen :8080 --web-dir web/dist --log-level debug

run-agent:
	go run ./cmd/agent/ --server http://localhost:8080 --token dev-token --device-id test-pc --log-level debug

# ── 清理 ──

clean:
	rm -rf bin/
	rm -rf web/dist/
	@echo "✅ 已清理"
