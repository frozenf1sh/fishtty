## 1. Protocol & 后端基础

- [x] 1.1 扩展 `proto/fishtty/v1/tunnel.proto` ErrorCode 枚举：新增 `ERROR_CODE_SESSION_LOST=8`, `ERROR_CODE_AGENT_UNREACHABLE=9`, `ERROR_CODE_CHANNEL_FULL=10`, `ERROR_CODE_CONNECTION_TIMEOUT=11`
- [x] 1.2 运行 `buf generate` 重新生成 Go 和 TypeScript 的 protobuf 代码
- [x] 1.3 修复 `internal/server/service/relay.go`：新增 `CleanSession(sid)` 方法，在 `RouteFromAgent` 收到 `SessionDestroyed` 时调用
- [x] 1.4 修复 `internal/server/adapter/connectrpc/handler.go`：在 recvLoop 中检测 `SessionDestroyed` 消息，调用 `relay.CleanSession()`
- [x] 1.5 在 `relay.go` 的 `RouteFromMobile` 中，Agent 不存在时向客户端发送 `ErrorMsg{code: AGENT_UNREACHABLE}`
- [x] 1.6 在 `relay.go` 的 `channelSender.SendMessage` 中，channel 满时向源端发送 `ErrorMsg{code: CHANNEL_FULL}`

## 2. 错误反馈 — 客户端可见性

- [ ] 2.1 重写 `web/src/ws/client.ts` 的 `ws.onerror` 回调：提取可用错误信息（event.message, event.type），构造包含描述的 Error 并调用 `callbacks.onError`
- [ ] 2.2 在 `doConnect()` 中添加 10 秒连接超时：使用 `setTimeout`，超时后主动 `ws.close()` 并调用 `callbacks.onError`
- [ ] 2.3 在 `web/src/App.tsx` 中处理所有新增 errorMsg 场景：`SESSION_LOST` → 自动创建新 Session；`AGENT_UNREACHABLE` → 显示设备离线提示；`CHANNEL_FULL` → 性能警告
- [ ] 2.4 增强 Toast 组件：支持不同错误级别（error/warning/info）的视觉样式，最多 3 条同时显示

## 3. 终端渲染 — WebGL & Unicode

- [ ] 3.1 安装依赖：`pnpm -C web add @xterm/addon-unicode11 @xterm/addon-canvas`
- [ ] 3.2 在 `Terminal.tsx` 中按优先级加载 addons：FitAddon → try WebglAddon → catch CanvasAddon → Unicode11Addon
- [ ] 3.3 实现 WebGL context loss 回退：监听 `webglAddon.onContextLoss()`，触发时替换为 CanvasAddon
- [ ] 3.4 在 `Terminal.tsx` 中使用 `term.parser.registerCsiHandler` 检测交替缓冲区切换（`\x1b[?1049h` / `\x1b[?1049l`）

## 4. 本地回显 — 交替缓冲区感知

- [ ] 4.1 在 `EchoBuffer` 中新增 `inAltBuffer` 状态标记
- [ ] 4.2 交替缓冲区进入时：清空 `pending` 缓冲区，设置 `inAltBuffer = true`
- [ ] 4.3 交替缓冲区退出时：设置 `inAltBuffer = false`，恢复正常回显匹配
- [ ] 4.4 在交替缓冲区内：`writeLocal` 不写入终端（vim 的响应不是简单字符回显），`drain` 直接透传服务端数据（不做前缀匹配）

## 5. 输入延迟 — rAF 批量发送

- [ ] 5.1 在 `Terminal.tsx` 的 `term.onData` 中实现 rAF 合并：累积输入到 `pendingInput` 数组，首个字符触发 `requestAnimationFrame(flush)`
- [ ] 5.2 `flush` 函数：合并 `pendingInput` 中所有 Uint8Array 为单个 DataChunk，调用 `client.sendData()`，清空数组
- [ ] 5.3 确保 `echo.writeLocal` 仍在每个字符到达时立即调用（本地回显不延迟）

## 6. 连接韧性 — Ping/Pong & 重连

- [ ] 6.1 在 `web/src/ws/client.ts` 中实现应用层 ping/pong：`setInterval` 每 30s 发送文本帧 "ping"
- [ ] 6.2 在 `internal/server/adapter/websocket/handler.go` 的读循环中检测文本帧 "ping" → 回复 "pong"
- [ ] 6.3 客户端添加 pong 超时检测：10s 未收到 pong → 主动 `ws.close()` 触发重连
- [ ] 6.4 实现重连风暴保护：60s 内重连 >5 次 → 退避上限提升至 30s 并显示持久 Toast

## 7. 状态持久化 & 自动恢复

- [ ] 7.1 在连接成功时将 `deviceId` 和 `fishtty_last_active` 写入 localStorage
- [ ] 7.2 在 `App.tsx` 的 `onopen` 处理中：若 `activeSessions` 为空且 localStorage 有 `fishtty_last_active`，自动调用 `createSession` + `client.createSession()`
- [ ] 7.3 session 创建成功时更新 localStorage 的 session 记录

## 8. 可观测性 & 服务端加固

- [ ] 8.1 新增 `/health` HTTP 端点：返回 JSON `{"status":"ok","agents":N,"mobiles":N,"sessions":N}`，Agent 全部离线时返回 503
- [ ] 8.2 增大 WebSocket upgrader 缓冲区：`ReadBufferSize` 和 `WriteBufferSize` 从 4096 → 65536
- [ ] 8.3 在 WebSocket handler 的读循环中添加 close code 检测，区分正常关闭/异常断开/协议错误并记录相应级别日志
- [ ] 8.4 为 docker-compose 中的 server 服务添加 `healthcheck` 指令（curl /health）
- [ ] 8.5 确认 `sessionOwners` 泄漏修复后，metrics 中的 `fishtty_active_sessions` 正确反映实时数据
