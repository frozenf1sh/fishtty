## Why

fishtty 当前面临两类阻断性问题：(1) 终端渲染错乱和输入延迟使 vim/Claude Code 等全屏 TUI 程序完全不可用——这是决定用户"想不想用"的核心体验问题；(2) 连接异常时客户端完全静默（黑屏无提示），用户无法判断是网络问题、会话丢失还是服务宕机。这两个问题叠加导致系统在真实使用场景（Android Chrome + 远程开发）下缺乏基本可用性。

## What Changes

### 止血层 — 错误可见性
- WebSocket `onerror` 不再静默，将具体错误（网络、TLS、协议）展示给用户
- 连接超时检测（10s 无响应则报错）
- Agent 不可达 / Session 不存在时，服务端主动推送 `errorMsg` 到客户端
- 重连后发现无活跃 Session，自动创建新终端（而非静默黑屏）
- 修复 Server 端 `sessionOwners` 内存泄漏（Session 销毁后未清理映射）

### 渲染层 — 终端正确性
- 加载 `@xterm/addon-webgl`（GPU 加速渲染），带 canvas 自动回退
- 加载 `@xterm/addon-unicode11`（CJK/emoji 列宽修正），修复网格错位
- 本地回显（EchoBuffer）增加交替缓冲区感知：检测 `\x1b[?1049h/l` 后暂停/恢复回显匹配
- WebSocket 读写缓冲区从 4KB 提升至 64KB

### 延迟层 — 输入响应
- 按键发送端使用 `requestAnimationFrame` 合并同一帧内的多次输入，减少 protobuf 序列化次数
- WebSocket 启用应用层 Ping/Pong（30s 间隔），提前检测死连接
- Relay channel 溢出时向客户端回传背压错误，避免静默丢数据

### 可维护性
- 服务端日志区分连接关闭原因（EOF / 超时 / 协议错误 / 正常关闭）
- 增加 `/health` 端点用于健康检查
- Agent 心跳超时后自动重连并恢复指标

## Capabilities

### New Capabilities
- `error-feedback`: 全链路错误可见性——WebSocket 层、会话层、中继层的错误均以 Toast 形式展示给用户，包含错误码和可操作的提示文案
- `terminal-rendering`: xterm.js 渲染栈升级——WebGL 加速、Unicode 列宽修正、交替缓冲区感知的本地回显
- `connection-resilience`: 连接韧性——超时检测、应用层心跳、背压通知、重连后自动恢复会话

### Modified Capabilities
- `protocol`: TunnelMessage 的 `errorMsg` 字段扩展覆盖场景（新增 `ERROR_CODE_SESSION_LOST`、`ERROR_CODE_AGENT_UNREACHABLE`、`ERROR_CODE_CHANNEL_FULL`）；`SessionDestroyed` 消息需触发 Server 端 `sessionOwners` 清理
- `architecture`: Server Relay 层增加 `sessionOwners` 生命周期管理（监听 `SessionDestroyed` 消息以清理映射）；Agent 心跳超时阈值从 3×15s 调整为可配置

## Impact

| 层面 | 影响 |
|------|------|
| `web/src/terminal/Terminal.tsx` | 重写：加载 WebGL/Unicode11 addon，交替缓冲区检测，rAF 批量发送 |
| `web/src/ws/client.ts` | 重写：onerror 报告，连接超时，应用层 ping/pong，重连后自动创建 session |
| `web/src/App.tsx` | 修改：新增 errorMsg 处理覆盖所有错误码，activeSessions 持久化到 localStorage |
| `web/package.json` | 新增依赖：`@xterm/addon-unicode11`、`@xterm/addon-canvas` |
| `internal/server/service/relay.go` | 修改：`sessionOwners` 清理逻辑，channel 满时发 errorMsg，路由失败时发 errorMsg |
| `internal/server/adapter/websocket/handler.go` | 修改：增大缓冲区，区分 close code，读取错误日志 |
| `internal/server/adapter/connectrpc/handler.go` | 修改：监听 `SessionDestroyed` 清理 relay |
| `internal/agent/service/tunnel.go` | 修改：心跳配置可参数化 |
| `proto/fishtty/v1/tunnel.proto` | 修改：ErrorCode 枚举扩展 |
| `deploy/` | 新增：`/health` 端点和 docker healthcheck |
