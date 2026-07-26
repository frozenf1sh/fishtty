# fishtty

通过公网中转远程控制无公网 IP 家用 PC 终端的系统。

```
┌─────────────────┐     ┌──────────────────────┐     ┌─────────────────┐
│  🖥 fishtty-agent │     │  🌐 fishtty-server    │     │  📱 fishtty-web  │
│  (家用 PC, NAT后) │◄───►│  (公网 VPS)           │◄───►│  (PWA 客户端)     │
│                  │     │                      │     │                  │
│  creack/pty      │     │  Connect-RPC + Relay │     │  xterm.js WebGL  │
│  128KB Ring Buf  │     │  WebSocket 网关       │     │  虚拟键盘栏       │
└─────────────────┘     └──────────────────────┘     └─────────────────┘
```

## 特性

- **零配置穿透 NAT**：Agent 主动发起反向长连接，无需端口映射或公网 IP
- **tmux 级断连恢复**：128 KB 环形缓冲区保存终端历史，重连时自动增量补发
- **多会话支持**：单 Agent 管理多个 PTY 终端（bash、zsh、claude code…）
- **移动端优化 PWA**：WebGL 渲染 + 虚拟键盘栏（Esc/Tab/Ctrl+C/方向键/粘贴）
- **二进制高效协议**：Protobuf + Connect-RPC，无 JSON/Base64 开销
- **单 Go 二进制部署**：Server 内嵌 PWA 静态文件，一个二进制即可运行

## 快速开始

### 1. 构建

```bash
# 安装工具链
go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest
corepack enable && pnpm install

# 生成协议代码 + 构建全部
make proto
make build
```

产物：
- `bin/fishtty-server` — 公网中继服务（内嵌 PWA）
- `bin/fishtty-agent`  — PC 端守护进程

## 配置管理

所有组件使用统一的 YAML 配置文件。优先级：**命令行 > 环境变量 > 配置文件 > 默认值**。

### Server 配置

参见 `configs/fishtty-server.yaml`：

```yaml
listen: ":8443"
log_level: "info"
# tls_cert: "/etc/fishtty/server.crt"   # 生产环境
# tls_key:  "/etc/fishtty/server.key"
```

### Agent 配置

参见 `configs/fishtty-agent.yaml`：

```yaml
server: "https://fishtty.example.com"
token: "your-device-token"
device_id: ""          # 留空 = 主机名
log_level: "info"

heartbeat:
  interval: 15s
  miss_threshold: 3

reconnect:
  min_delay: 1s
  max_delay: 60s
  reset_after: 30s

ring_buffer:
  size_kb: 128
```

环境变量可覆盖任意配置项：`FISHTTY_TOKEN`、`FISHTTY_SERVER`、`FISHTTY_LOG_LEVEL` 等。

---

### 2. 部署 Server（公网 VPS）

**Docker Compose（推荐）**：

```bash
# 1. 编辑配置
cp configs/fishtty-server.yaml configs/fishtty-server.yaml
vim configs/fishtty-server.yaml   # 改 listen/log_level

# 2. 编辑 Caddyfile 域名
vim Caddyfile                     # 替换 <your-domain.com>

# 3. 启动
docker compose up -d

# 4. 查看日志
docker compose logs -f server
```

**直接运行（开发/内网）**：

```bash
# 默认配置 + 命令行覆盖
./bin/fishtty-server --listen :8080 --log-level debug

# 指定配置文件
./bin/fishtty-server --config /etc/fishtty/server.yaml

# 启用 TLS
./bin/fishtty-server --config server.yaml --tls-cert server.crt --tls-key server.key
```

### 3. 部署 Agent（家用 PC）

**Linux（systemd 开机自启）**：

```bash
# 1. 安装二进制 + 配置
sudo cp bin/fishtty-agent /usr/local/bin/
sudo mkdir -p /etc/fishtty
sudo cp configs/fishtty-agent.yaml /etc/fishtty/

# 2. 编辑配置（填入你的 server 地址和 token）
sudo vim /etc/fishtty/fishtty-agent.yaml

# 3. 安装并启动服务
sudo cp deploy/fishtty-agent.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now fishtty-agent

# 4. 查看状态
systemctl status fishtty-agent
journalctl -u fishtty-agent -f
```

**macOS（launchd 开机自启）**：

```bash
# 1. 安装二进制 + 配置
sudo cp bin/fishtty-agent /usr/local/bin/
mkdir -p ~/.config/fishtty
cp configs/fishtty-agent.yaml ~/.config/fishtty/

# 2. 编辑配置 + plist（替换用户名）
vim ~/.config/fishtty/fishtty-agent.yaml
# 编辑 deploy/com.fishtty.agent.plist，把 REPLACE_USERNAME 改成你的用户名

# 3. 安装并启动
cp deploy/com.fishtty.agent.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.fishtty.agent.plist

# 4. 查看日志
tail -f /usr/local/var/log/fishtty-agent.log
```

**直接运行（调试）**：

```bash
./bin/fishtty-agent \
  --server https://your-server.example.com \
  --token my-device-token
# 或使用配置文件
./bin/fishtty-agent --config ./agent.yaml
```

### 4. 连接移动端

1. 手机浏览器打开 `https://your-server.example.com`
2. 输入设备 ID 和 Server 地址
3. 点击「连接设备」→「+ 终端」即可开始远程控制

添加到主屏幕可获得全屏沉浸式 PWA 体验。

## 架构

```
Agent (Connect-RPC 客户端)
  │
  │  FishTTY.Tunnel(bidi stream)
  │  ├── AuthRequest → AuthResponse
  │  ├── SessionInit → SessionCreated
  │  ├── DataChunk (PTY stdout, seq-numbered)
  │  ├── Heartbeat → HeartbeatAck
  │  └── Reattach (delta replay from ring buffer)
  │
  ▼
Server (Relay)
  │
  │  WebSocket /ws
  │  ├── Binary frames (TunnelMessage protobuf)
  │  └── Sub-protocol: fish-tty-v1
  │
  ▼
Mobile PWA (React + xterm.js)
```

详细设计文档见 `openspec/specs/architecture.md` 和 `openspec/specs/protocol.md`。

## 目录结构

```
fishpts/
├── cmd/
│   ├── agent/main.go       # Agent 入口
│   └── server/main.go      # Server 入口（内嵌 PWA）
├── internal/
│   ├── agent/              # Agent 核心（pty/ringbuffer/session/tunnel）
│   └── server/             # Server 核心（auth/relay/ws/tunnel_handler）
├── proto/fishtty/v1/       # Protobuf 协议定义
├── gen/                    # 生成的 Go/TS 代码
├── web/                    # PWA 前端
│   └── src/
│       ├── ws/client.ts    # WebSocket + Protobuf 客户端
│       ├── terminal/       # xterm.js 组件 + 虚拟键盘
│       └── sessions/       # Session 状态管理
├── test/                   # 集成测试
├── buf.yaml                # Buf 配置
├── Makefile                # 构建脚本
├── Dockerfile              # 多阶段构建
└── docker-compose.yml      # 生产部署编排
```

## 技术栈

| 组件 | 技术 |
|------|------|
| 协议 | Protobuf + Connect-RPC (Connect-Go) |
| Agent→Server | Connect-RPC Bidirectional Stream over HTTP/2 |
| Mobile→Server | WebSocket Binary Frames + Protobuf |
| PTY 管理 | `github.com/creack/pty` |
| 前端 | React 19 + xterm.js 5 + WebGL Addon |
| 构建 | Buf CLI + Vite + pnpm |

## 本地开发与端到端测试

fishtty 支持纯 HTTP (h2c) 模式，零配置即可在本机跑通全部组件。

### 前置条件

- Go 1.23+
- Node.js 20+ / pnpm
- (macOS/Linux，PTY 需要 Unix)

### 一键启动

```bash
# 1. 构建前端（首次需要）
cd web && pnpm install && pnpm run build && cd ..

# 2. 终端 1：启动 Server（启用 h2c，支持 Connect-RPC 双向流）
go run ./cmd/server/ --listen :8080 --web-dir web/dist --log-level debug

# 3. 终端 2：启动 Agent（通过 h2c 连接 Server）
go run ./cmd/agent/ --server http://localhost:8080 --token dev-token --device-id test-pc --log-level debug

# 4. 浏览器打开 http://localhost:8080
#    → 输入设备 ID: test-pc → 点击连接 → 点击 + 终端
#    → 应该看到 shell 提示符（Agent 在本地创建了 PTY）
```

### h2c 说明

Server 在无 TLS 模式下自动启用 Go 1.24+ 内建的 `UnencryptedHTTP2`，
Agent 使用 `http2.Transport{AllowHTTP: true}` 建立 HTTP/2 Cleartext 连接。
这使得 Connect-RPC 双向流在纯 HTTP 环境下正常工作。

生产环境通过 Caddy/Nginx 做 TLS 终止，外部 HTTPS，
内部到 Server 的结构保持不变。

## 开发命令

```bash
make proto        # 生成 Protobuf 代码
make build-web    # 构建前端
make build        # 构建全部（web + server + agent）
make test         # 运行全部 27 个测试
make run-server   # 启动开发 Server (localhost:8080, h2c 模式)
make run-agent    # 启动开发 Agent（连接 localhost:8080）
make docker       # 构建 Docker 镜像
make lint         # 代码检查（buf + vet + tsc）
```

## 许可证

MIT
