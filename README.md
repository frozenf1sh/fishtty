# fishtty

<p align="center">
  <strong>专为无公网 IP 家用设备打造的高性能、轻量级 Web 远程终端系统</strong>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go" alt="Go Version">
  <img src="https://img.shields.io/badge/Frontend-React_19_|_xterm.js-61DAFB?style=flat&logo=react" alt="Frontend Stack">
  <img src="https://img.shields.io/badge/Protocol-Connect--RPC_|_Protobuf-37474F?style=flat" alt="Protocol">
  <img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License">
</p>

---

**fishtty** 是一款通过公网 VPS 中转、实现反向穿透远程控制局域网 PC/服务器的终端系统。无需公网 IP，无需配置路由器端口映射，即可通过浏览器（尤其是移动端 PWA）获得原生般的终端操作体验。

```

┌─────────────────────────┐          ┌─────────────────────────┐          ┌─────────────────────────┐
│     🖥️ fishtty-agent     │          │    🌐 fishtty-server    │          │      📱 fishtty-web     │
│   (受控端 PC / NAT 后)   │◄────────►│       (公网 VPS)        │◄────────►│       (PWA 客户端)      │
│                         │  HTTP/2  │                         │ WebSocket│                         │
│  • creack/pty           │ Connect- │  • Connect-RPC Relay    │ Protocol │  • xterm.js (WebGL)     │
│  • 128KB Ring Buffer    │   Stream │  • Web Gateway / Embedded│  v1      │  • Virtual Keyboard     │
└─────────────────────────┘          └─────────────────────────┘          └─────────────────────────┘

```

---

## ✨ 核心特性

- **⚡️ 零配置 NAT 穿透**：Agent 主动向公网发起反向长连接（HTTP/2 双向流），轻松穿透多层 NAT。
- **🔄 tmux 级断连无缝恢复**：内置 128 KB 环形缓冲区（Ring Buffer）保留终端历史，网络波动重连后自动**增量补发**缺失数据。
- **🔀 多会话多终端并发**：单 Agent 实例支持管理多个 PTY 终端（Bash、Zsh、Claude Code 等）。
- **📱 移动端深度优化**：支持 WebGL 高性能渲染，集成针对手机端定制的虚拟按键栏（包含 `Esc`、`Tab`、`Ctrl+C`、方向键、剪贴板等）。
- **🚀 二进制极简高效协议**：基于 Protobuf + Connect-RPC 传输，相比传统 JSON/Base64 方案节省开销且性能更高。
- **📦 单二进制开箱即用**：Server 端完美内嵌 PWA 静态资源，单个 Go 二进制文件即可快速部署上线。

---

## 🚀 一键安装

选择对应系统的命令执行，即可将 `fishtty-agent` 安装到 `/usr/local/bin/`：

### Linux (x86_64)

```bash
curl -fsSL "https://github.com/frozenf1sh/fishtty/releases/latest/download/fishtty-agent-linux-amd64.tar.gz" | sudo tar -xz -C /usr/local/bin/ "./fishtty-agent" && sudo chmod +x /usr/local/bin/fishtty-agent
```

### Linux (ARM64, 树莓派等)

```bash
curl -fsSL "https://github.com/frozenf1sh/fishtty/releases/latest/download/fishtty-agent-linux-arm64.tar.gz" | sudo tar -xz -C /usr/local/bin/ "./fishtty-agent" && sudo chmod +x /usr/local/bin/fishtty-agent
```

### macOS (Apple Silicon)

```bash
curl -fsSL "https://github.com/frozenf1sh/fishtty/releases/latest/download/fishtty-agent-darwin-arm64.tar.gz" | sudo tar -xz -C /usr/local/bin/ "./fishtty-agent" && sudo chmod +x /usr/local/bin/fishtty-agent
```

安装后继续查看 [部署 Agent](#3-部署-agent受控端-pc) 完成配置文件和服务注册。

---

## 🔄 一键升级

### Linux (systemd)

```bash
curl -fsSL "https://github.com/frozenf1sh/fishtty/releases/latest/download/fishtty-agent-linux-amd64.tar.gz" | sudo tar -xz -C /usr/local/bin/ "./fishtty-agent" && sudo chmod +x /usr/local/bin/fishtty-agent && sudo systemctl restart fishtty-agent
```

> ARM64 用户将上面命令中的 `linux-amd64` 替换为 `linux-arm64` 即可。

### macOS (launchd)

```bash
curl -fsSL "https://github.com/frozenf1sh/fishtty/releases/latest/download/fishtty-agent-darwin-arm64.tar.gz" | sudo tar -xz -C /usr/local/bin/ "./fishtty-agent" && sudo chmod +x /usr/local/bin/fishtty-agent && launchctl unload ~/Library/LaunchAgents/com.fishtty.agent.plist && launchctl load ~/Library/LaunchAgents/com.fishtty.agent.plist
```

---

## 目录

- [✨ 核心特性](#-核心特性)
- [🚀 一键安装](#-一键安装)
- [🔄 一键升级](#-一键升级)
- [🏗️ 技术架构](#️-技术架构)
- [🚀 快速开始](#-快速开始)
  - [1. 编译构建](#1-编译构建)
  - [2. 部署 Server（公网 VPS）](#2-部署-server公网-vps)
  - [3. 部署 Agent（受控端 PC）](#3-部署-agent受控端-pc)
  - [4. 移动端 / Web 访问](#4-移动端--web-访问)
- [⚙️ 配置说明](#️-配置说明)
- [🛠️ 本地开发与 E2E 测试](#️-本地开发与-e2e-测试)
- [🛠️ 常用开发命令](#️-常用开发命令)
- [🧰 技术栈](#-技术栈)
- [📄 许可证](#-许可证)

---

## 🏗️ 技术架构

```

Agent (Connect-RPC Client)
│
│  FishTTY.Tunnel (Bidirectional Stream over HTTP/2)
│  ├── AuthRequest → AuthResponse
│  ├── SessionInit → SessionCreated
│  ├── DataChunk (PTY stdout, seq-numbered)
│  ├── Heartbeat → HeartbeatAck
│  └── Reattach (delta replay from ring buffer)
▼
Server (Relay & Gateway)
│
│  WebSocket (/ws - Sub-protocol: fish-tty-v1)
│  └── Binary Frames (TunnelMessage protobuf)
▼
Mobile PWA (React + xterm.js)

```

> [!NOTE]
> 详细设计与协议文档请参阅 [`openspec/specs/architecture.md`](openspec/specs/architecture.md) 与 [`openspec/specs/protocol.md`](openspec/specs/protocol.md)。

---

## 🚀 快速开始

### 1. 编译构建

**前置需求**：Go 1.23+，Node.js 20+，pnpm，Buf CLI。

```bash
# 1. 安装工具链
go install [connectrpc.com/connect/cmd/protoc-gen-connect-go@latest](https://connectrpc.com/connect/cmd/protoc-gen-connect-go@latest)
corepack enable && pnpm install

# 2. 生成协议代码 & 编译全部产物
make proto
make build

```

编译产物说明：

- `bin/fishtty-server`：公网中继服务端（已内置 PWA 前端资源）
- `bin/fishtty-agent`：受控端守护进程

---

### 2. 部署 Server（公网 VPS）

#### 方式 A：Docker Compose（推荐）

```bash
# 1. 准备 Server 配置文件
cp configs/fishtty-server.yaml configs/fishtty-server.yaml
vim configs/fishtty-server.yaml

# 2. 配置域名与 TLS 终止（编辑 Caddyfile 替换 <your-domain.com>）
vim Caddyfile

# 3. 启动容器
docker compose up -d

# 4. 查看运行日志
docker compose logs -f server

```

#### 方式 B：直接运行二进制

```bash
# 1. 使用默认配置 + 命令行覆盖
./bin/fishtty-server --listen :8080 --log-level debug

# 2. 指定配置文件运行
./bin/fishtty-server --config /etc/fishtty/server.yaml

# 3. 开启原生 TLS 监听
./bin/fishtty-server --config server.yaml --tls-cert server.crt --tls-key server.key

```

---

### 3. 部署 Agent（受控端 PC）

> 二进制安装请使用上方 [一键安装](#-一键安装) 命令，以下为配置文件与服务注册步骤。

#### 配置文件

下载并编辑配置文件，填入你的 VPS 地址和认证 Token：

```bash
# Linux
sudo mkdir -p /etc/fishtty
sudo curl -fsSL "https://raw.githubusercontent.com/frozenf1sh/fishtty/main/configs/fishtty-agent.yaml" -o /etc/fishtty/fishtty-agent.yaml
sudo vim /etc/fishtty/fishtty-agent.yaml

# macOS
sudo mkdir -p /usr/local/etc/fishtty
sudo curl -fsSL "https://raw.githubusercontent.com/frozenf1sh/fishtty/main/configs/fishtty-agent.yaml" -o /usr/local/etc/fishtty/fishtty-agent.yaml
sudo vim /usr/local/etc/fishtty/fishtty-agent.yaml
```

> 也可从 [Release 包](https://github.com/frozenf1sh/fishtty/releases) 中解压获取配置模板：`tar -xz "./fishtty-agent.yaml"`。

#### 从源码构建

如需自行编译：

```bash
make deploy-agent-all                 # 全平台
make deploy-agent-linux-amd64         # Linux x86_64
make deploy-agent-linux-arm64         # Linux ARM64 (树莓派)
make deploy-agent-darwin-arm64        # macOS Apple Silicon
```

构建产物保存在 `deploy/` 目录下。

#### Linux 部署（systemd 守护进程）

```bash
# 1. 运行一键安装脚本
sudo ./install.sh

# 2. 修改配置
sudo vim /etc/fishtty/fishtty-agent.yaml

# 3. 启动并设置开机自启
sudo systemctl enable --now fishtty-agent

```

```bash
systemctl status fishtty-agent    # 查看服务状态
journalctl -u fishtty-agent -f     # 实时查看日志
systemctl restart fishtty-agent    # 重启服务
systemctl stop fishtty-agent       # 停止服务

```

#### macOS 部署（launchd 守护进程）

```bash
# 1. 执行安装
./install.sh

# 2. 修改配置
vim /usr/local/etc/fishtty/fishtty-agent.yaml

# 3. 加载并启动服务
launchctl load ~/Library/LaunchAgents/com.fishtty.agent.plist

```

```bash
launchctl list | grep fishtty                  # 查看服务运行状态
tail -f /usr/local/var/log/fishtty-agent.log   # 实时查看日志
launchctl unload ~/Library/LaunchAgents/com.fishtty.agent.plist # 停止服务

```

---

### 4. 移动端 / Web 访问

1. 手机或电脑浏览器打开 `https://your-server.example.com`。
2. 输入配置的 **设备 ID** 与 **Server 地址**。
3. 点击 **「连接设备」** → **「+ 新增终端」** 即可实时操控远端 Shell。
4. **推荐**：在 iOS Safari 或 Android Chrome 中选择 **“添加到主屏幕”**，获得全屏无边框的 PWA 沉浸体验。

---

## ⚙️ 配置说明

所有组件支持使用统一格式的 YAML 配置文件。

配置覆盖优先级：**命令行参数 > 环境变量 > 配置文件 > 默认值**。

### Server 配置范例 (`fishtty-server.yaml`)

| 配置项 | 说明 | 示例 / 默认值 |
| --- | --- | --- |
| `listen` | 监听地址及端口 | `":8443"` |
| `log_level` | 日志级别 (`debug`, `info`, `warn`, `error`) | `"info"` |
| `tls_cert` | TLS 证书路径（可选，推荐由反向代理统一处理） | `"/etc/fishtty/server.crt"` |
| `tls_key` | TLS 私钥路径 | `"/etc/fishtty/server.key"` |

### Agent 配置范例 (`fishtty-agent.yaml`)

```yaml
server: "[https://fishtty.example.com](https://fishtty.example.com)" # Server 端访问地址
token: "your-device-token"            # 鉴权 Token
device_id: ""                         # 设备唯一标识（留空自动使用 Hostname）
log_level: "info"

heartbeat:
  interval: 15s                       # 心跳检测间隔
  miss_threshold: 3                   # 允许丢失心跳的最大次数

reconnect:
  min_delay: 1s                       # 退避重连最小等待时间
  max_delay: 60s                      # 退避重连最大等待时间
  reset_after: 30s                    # 连接稳定运行多长时间后重置退避计数

ring_buffer:
  size_kb: 128                        # 终端增量历史环形缓冲区大小 (KB)

```

> [!TIP]
> 支持通过环境变量直接覆盖任意配置项，例如：`FISHTTY_TOKEN=my-secret-token` 或 `FISHTTY_SERVER=https://vps.example.com`。

---

## 🛠️ 本地开发与 E2E 测试

`fishtty` 支持原生 **h2c (Unencrypted HTTP/2)** 模式，无需配置复杂的 TLS 证书即可在本地完成全链路联调。

### 快速本地启动

```bash
# 1. 编译前端（首次运行或修改 web 代码后执行）
cd web && pnpm install && pnpm run build && cd ..

# 2. 终端 1：启动 Server (开启 h2c 模式并加载本地 PWA 静态资源)
go run ./cmd/server/ --listen :8080 --web-dir web/dist --log-level debug

# 3. 终端 2：启动 Agent (通过 h2c 连接本地 Server)
go run ./cmd/agent/ --server http://localhost:8080 --token dev-token --device-id test-pc --log-level debug

# 4. 浏览器访问 http://localhost:8080
#    -> 输入设备 ID: test-pc -> 点击连接 -> 新建终端

```

> **原理说明**：当无 TLS 配置时，Server 会自动启用 Go 内部的 `UnencryptedHTTP2` 特性，Agent 则建立 `HTTP/2 Cleartext` 连接，确保 Connect-RPC 双向流在纯 HTTP 环境下依然能够高吞吐运行。

---

## 🛠️ 常用开发命令

| 命令 | 说明 |
| --- | --- |
| `make proto` | 根据 `.proto` 文件重新生成 Go/TS 代码 |
| `make build-web` | 仅编译前端 PWA 项目 |
| `make build` | 完整构建 Web 资产并生成 Server / Agent 二进制文件 |
| `make test` | 运行单元测试与集成测试 |
| `make run-server` | 快捷启动开发版 Server (`localhost:8080`) |
| `make run-agent` | 快捷启动开发版 Agent |
| `make docker` | 构建 Docker 镜像 |
| `make lint` | 静态代码分析与类型检查 (buf + vet + tsc) |

---

## 🧰 技术栈

| 模块 | 关键技术 | 说明 |
| --- | --- | --- |
| **通讯协议** | Protobuf + Connect-RPC | 基于 HTTP/2 的轻量级 RPC 框架 |
| **Agent 传输** | Connect-Go Stream | Bidirectional Stream over HTTP/2 |
| **前端传输** | WebSocket + Protobuf | 二进制传输，极大降低 Protocol 开销 |
| **PTY 驱动** | `creack/pty` | Go 语言原生 POSIX Terminal / Pseudo-TTY 绑定 |
| **Web 前端** | React 19 + Vite | PWA 响应式架构 |
| **终端渲染** | xterm.js 5 + WebGL Addon | GPU 加速终端文本与动画渲染 |

---

## 📄 许可证

本项目基于 [MIT 许可证](https://www.google.com/search?q=LICENSE) 开源。
