# fishtty System Architecture

## Overview

fishtty is a remote terminal system enabling control of a home PC (behind NAT) from a mobile device through a public relay server. Three components collaborate:

```
┌──────────────────────┐       ┌──────────────────────────────┐       ┌──────────────────────┐
│   🖥 fishtty-agent    │       │     🌐 fishtty-server         │       │   📱 fishtty-web      │
│   (家用 PC, NAT 后)   │       │     (公网 VPS / Cloud)        │       │   (PWA 客户端)         │
│                      │       │                              │       │                      │
│  ┌────────────────┐  │       │  ┌─────────────────────────┐ │       │  ┌──────────────────┐ │
│  │ Session Manager │  │       │  │   Device Registry       │ │       │  │ React SPA        │ │
│  │                │  │       │  │   - Device auth          │ │       │  │ - Device List    │ │
│  │ ┌────────────┐ │  │       │  │   - Heartbeat tracking   │ │       │  │ - Session Tabs   │ │
│  │ │ bash (pts) │ │  │       │  └─────────────────────────┘ │       │  │ - Virtual KB Bar │ │
│  │ │ ring buf   │ │  │       │                              │       │  └────────┬─────────┘ │
│  │ └────────────┘ │  │       │  ┌─────────────────────────┐ │       │           │           │
│  │ ┌────────────┐ │  │       │  │   Stream Relay          │ │       │  WebSocket│           │
│  │ │claude(pts) │ │  │       │  │   - Route by session_id  │ │       │  Binary   │           │
│  │ │ ring buf   │ │  │       │  │   - Protocol bridge      │ │       │  Protobuf │           │
│  │ └────────────┘ │  │       │  │   (Connect-RPC ↔ WS)     │ │◄──────┼───────────┘           │
│  │                │  │       │  └────────────┬────────────┘ │       │                      │
│  └───────┬────────┘  │       │               │              │       │                      │
│          │           │       │               │              │       │                      │
│    creack/pty        │       │  Connect-RPC  │              │       │                      │
│    (PTY master fd)   │       │  Binary Proto │              │       │                      │
│          │           │       │               │              │       │                      │
└──────────┼───────────┘       └───────────────┼──────────────┘       └──────────────────────┘
           │                                   │
           │  Agent initiates TLS connection   │
           │  (the only side that can dial)    │
           └───────────────────────────────────┘
```

## Component Responsibilities

### fishtty-server

- **Deployment**: Public VPS with TLS termination (Caddy/Nginx).
- **Device Registry**: Stores device identities, auth tokens, online/offline status. In-memory for v1; SQLite for persistence.
- **Stream Relay**: Accepts one Connect-RPC bidirectional stream from each Agent. Accepts one WebSocket connection from each mobile client. Routes `TunnelMessage` objects between them by `session_id`.
- **Auth**: Validates device tokens. Issues session tokens for mobile clients (v1: pre-shared keys).
- **Observability**: Exposes Prometheus metrics and structured logging.

### fishtty-agent

- **Deployment**: User's home PC or server (Linux/macOS). Single Go binary, systemd/launchd service.
- **Tunnel Client**: Connect-RPC client. Dials the Server on startup with exponential backoff. Maintains exactly one long-lived bidirectional stream.
- **Session Manager**: Creates and manages multiple PTY sessions. Each session runs a shell or command via `creack/pty`, with a 128 KB ring buffer for output history.
- **Heartbeat**: Sends `Heartbeat` every 15s. Declares connection dead after 3 missed ACKs.

### fishtty-web

- **Deployment**: Static assets served by the Server or a CDN. Single-page React application.
- **Terminal UI**: `@xterm/xterm` with `@xterm/addon-webgl` (GPU rendering) and `@xterm/addon-fit` (responsive resize).
- **WebSocket Client**: Binary WebSocket with Protobuf message serialization. Tracks `last_ack_seq` per session for reattach.
- **Virtual Keyboard Bar**: Persistent toolbar with Esc, Tab, Arrows, Ctrl+C, Paste buttons.
- **PWA**: Service worker for offline shell, web manifest for installability.

## Data Flow

### Session Creation

```
Mobile                          Server                         Agent
  │                               │                              │
  │  SessionInit{cols,rows,cmd}   │                              │
  ├──────────────────────────────►│                              │
  │                               │  SessionInit{cols,rows,cmd}  │
  │                               ├─────────────────────────────►│
  │                               │                              │── pty.StartWithSize(cmd)
  │                               │                              │── allocate ring buffer (128KB)
  │                               │  SessionCreated{session_id}  │
  │                               │◄─────────────────────────────┤
  │  SessionCreated{session_id}   │                              │
  │◄──────────────────────────────┤                              │
  │                               │                              │
  │  xterm.js opens terminal      │                              │
```

### Terminal I/O

```
Mobile                          Server                         Agent
  │                               │                              │
  │  DataChunk{session_id, data}  │                              │
  ├──────────────────────────────►│                              │
  │                               │  DataChunk{session_id, data} │
  │                               ├─────────────────────────────►│
  │                               │                              │── ptmx.Write(data)
  │                               │                              │
  │                               │                              │── data := ptmx.Read(buf)
  │                               │  DataChunk{session_id,       │
  │                               │            seq, data}        │
  │                               │◄─────────────────────────────┤
  │  DataChunk{session_id,        │                              │
  │            seq, data}         │                              │
  │◄──────────────────────────────┤                              │
  │                               │                              │
  │  xterm.write(data)            │                              │
  │  last_ack_seq = seq           │                              │
```

### Reattach on Reconnect

```
Mobile                          Server                         Agent
  │                               │                              │
  │  (WebSocket reconnected)      │                              │
  │                               │                              │
  │  Reattach{session_id,         │                              │
  │           last_ack_seq: 42}   │                              │
  ├──────────────────────────────►│                              │
  │                               │  Reattach{session_id,        │
  │                               │           last_ack_seq: 42}  │
  │                               ├─────────────────────────────►│
  │                               │                              │── scan ring buffer
  │                               │                              │   for chunks seq>42
  │                               │  ReattachData{start_seq,     │
  │                               │              chunks=[43..50]}│
  │                               │◄─────────────────────────────┤
  │  ReattachData{start_seq: 43,  │                              │
  │               chunks=[43..50]}│                              │
  │◄──────────────────────────────┤                              │
  │                               │                              │
  │  xterm.write(chunk.data)      │                              │
  │  for each chunk in order      │                              │
  │                               │                              │
  │  (resume live streaming)      │                              │
  │  DataChunk{seq: 51, data}     │                              │
  │◄──────────────────────────────┤                              │
```

## Protocol Stack

```
┌──────────────────────┐
│   TunnelMessage       │  Protobuf message (oneof)
│   (Application)       │
├──────────────────────┤
│   Connect Protocol    │  Agent↔Server: Connect-RPC binary
│   or WebSocket Binary │  Mobile↔Server: WebSocket frames
├──────────────────────┤
│   HTTP/1.1 or HTTP/2  │  Connect-RPC works over both
│   (with TLS)          │  WebSocket over TLS
├──────────────────────┤
│   TCP                 │
└──────────────────────┘
```

## Security Model

- **Agent→Server**: Mutual TLS (Server presents certificate; Agent can be configured with certificate pinning). Auth token sent as first message on stream.
- **Mobile→Server**: TLS (standard HTTPS/WebSocket). Auth via pre-shared device token or session token issued by Server.
- **Data at rest**: None. Terminal data is not persisted on Server. Ring buffers on Agent are in-memory only.
- **Trust boundary**: The Server is trusted. It sees all PTY data in plaintext. Future: end-to-end encryption between Mobile and Agent with Server as opaque relay.

## Concurrency Model

### Agent Goroutines per Session

```
Session "abc123"
├── readLoop goroutine: ptmx.Read() → ringBuffer.Write() → stream.Send(DataChunk)
├── writeLoop goroutine: stream.Recv() → filter by session_id → ptmx.Write()
└── (PTY process: fork+exec'd child, monitored via Wait4/syscall)
```

The readLoop and writeLoop are independent. The readLoop blocks on `ptmx.Read()` (yielding the goroutine when idle). The writeLoop blocks on `stream.Recv()`. Both exit when the session is destroyed or the Agent disconnects.

### Server Goroutines per Connection

```
Agent Connection
├── agentRecvLoop: stream.Recv() → route to mobile by session_id → ws.WriteMessage()
├── agentSendLoop: ws.ReadMessage() → route by session_id → stream.Send()
└── Mobile connections: one per connected client
    ├── mobileRecvLoop: ws.ReadMessage() → route to agent → stream.Send()
    └── mobileSendLoop: stream.Recv() → route to mobile → ws.WriteMessage()
```

## Session Ownership Lifecycle

Server 的 Stream Relay 维护 `sessionOwners` 映射以追踪活跃会话。该映射在以下时机更新：

- **Session 创建**: `SessionCreated` 消息到达时建立 ownership
- **Session 销毁**: `SessionDestroyed` 消息到达时调用 `CleanSession(sid)` 清理
- **Agent 断开**: `UnregisterAgent` 时批量清理该设备的所有 session 条目

```go
// relay.go — CleanSession 清理指定会话的 ownership 映射
func (r *Relay) CleanSession(sid string) {
    r.mu.Lock(); defer r.mu.Unlock()
    delete(r.sessionOwners, sid)
    delete(r.pendingInits, sid)
}
```

Server Relay 在以下场景主动向消息源端发送错误：
- `RouteFromMobile` 找不到目标 Agent → 发送 `ErrorMsg{code: AGENT_UNREACHABLE}`
- `channelSender.SendMessage` channel 满 → 发送 `ErrorMsg{code: CHANNEL_FULL}`

## WebSocket 配置

- **读写缓冲区**: 65536 字节（从 4096 提升），减少大帧输出时的分片和系统调用
- **应用层 Ping/Pong**: 客户端每 30s 发送文本帧 "ping"，服务端立即回复 "pong"，不经过 protobuf 序列化
- **关闭原因日志**: 区分正常关闭 (1000)、页面离开 (1001)、异常断开，记录对应级别的日志

## Health Check 端点

Server 提供 `/health` HTTP 端点，返回 JSON 格式的健康状态：

```json
{"status":"ok","agents":1,"mobiles":1,"sessions":1}
```

- Agent 全部离线时返回 503 和 `"status":"degraded"`
- 响应时间 <10ms

## Agent Heartbeat 配置

Agent 的心跳检测支持通过配置文件调整参数：

```yaml
heartbeat:
  interval: 15s        # 心跳发送间隔
  miss_threshold: 3     # 连续未收到 ACK 的次数阈值（45s 超时）
```

## Web 前端渲染栈

xterm.js 按以下优先级加载 addons：

| 优先级 | Addon | 用途 |
|--------|-------|------|
| 1 | `@xterm/addon-fit` | 自适应容器尺寸 |
| 2 | `@xterm/addon-webgl` | GPU 加速渲染（主渲染器） |
| 3 | `@xterm/addon-canvas` | Canvas 回退（WebGL 不可用时） |
| 4 | `@xterm/addon-unicode11` | Unicode 11 列宽修正 |

WebGL context 丢失时自动降级到 Canvas。Unicode11 addon 修正 CJK/emoji 等宽字符的列宽计算。

## Web 前端 WebSocket 客户端

- 连接超时：10 秒无响应则报错并触发重连
- 应用层心跳：30s ping/pong，10s pong 超时触发重连
- 重连风暴保护：60s 内 >5 次断连 → 退避上限提升至 30s
- 状态持久化：`deviceId` 和活跃 session 信息写入 localStorage
- 自动恢复：重连后无 session 时自动创建新终端

## References

- `openspec/specs/protocol.md` — Protobuf schema and connection state machine
- `openspec/specs/terminal-rendering.md` — 终端渲染规格
- `openspec/specs/error-feedback.md` — 错误反馈规格
- `openspec/specs/connection-resilience.md` — 连接韧性规格
- `openspec/changes/fishtty-architecture/design.md` — Detailed design decisions
