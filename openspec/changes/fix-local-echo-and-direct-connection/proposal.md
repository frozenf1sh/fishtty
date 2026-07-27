## Why

去掉 Cloudflare Tunnel 后延迟从 ~200ms 骤降到 ~5ms，直连验证了架构可行性。但低延迟暴露了 EchoBuffer 字符匹配的竞态缺陷：服务端回显在 rAF flush 前就到达，导致"打一个 l 出两个 l"。同时需要正式替代 CF Tunnel 的直连部署方案，以及关闭 WebSocket 压缩、增大 PTY buffer 等配套的延迟优化。

## What Changes

### 双击键修复 — EchoBuffer 重写
- 废弃当前的逐字符前缀匹配算法
- 改用**本地序号追踪**：每次 `writeLocal` 递增序号，服务端回显 DataChunk 携带 `echo_seq`，drain 时按序号吸收匹配的本地写入
- 在低延迟（<10ms RTT）和高延迟（>100ms RTT）下均行为正确

### 直连部署
- ECS 上关闭 cloudflared tunnel，用户通过 `http://<ip>:8001` 直连
- Server 的 HTTP 响应头增加 `Access-Control-Allow-Origin` 以支持 PWA 跨域场景
- docker-compose 精简为单 server 服务（去掉 CF 依赖）

### 延迟优化
- 关闭 WebSocket per-message deflate 压缩（`EnableCompression: false`），省 3-10ms CPU
- Agent PTY read buffer 从 4096 提升至 32768 字节，减少大输出时的消息分片和 syscall 次数
- 可选：调整 rAF 发送阈值从每帧改为 5ms 定时器（更快 flush）

## Capabilities

### New Capabilities
- `direct-connection`: 直连部署模式——不经 CF Tunnel，PWA 直连 Server IP:Port，包含 CORS 支持和延迟优化配置

### Modified Capabilities
- `terminal-rendering`: EchoBuffer 从逐字符前缀匹配改为本地序号追踪，消除低延迟下的竞态双击键

## Impact

| 层面 | 影响 |
|------|------|
| `web/src/terminal/Terminal.tsx` | 重写 EchoBuffer：序号追踪替代字符匹配 |
| `web/src/ws/client.ts` | 新增 `echoSeq` 字段跟随 DataChunk 发送 |
| `proto/fishtty/v1/tunnel.proto` | DataChunk 新增 `echo_seq` 字段（**BREAKING** wire format） |
| `internal/server/adapter/websocket/handler.go` | `EnableCompression: false` |
| `internal/agent/service/session.go` | read buffer 4096 → 32768 |
| `deploy/` | 关闭 cloudflared，docker-compose 精简 |
