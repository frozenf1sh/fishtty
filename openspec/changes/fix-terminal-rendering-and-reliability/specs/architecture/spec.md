## MODIFIED Requirements

### Requirement: fishtty-server — Stream Relay
Server 的 Stream Relay SHALL 维护 `sessionOwners` 映射的正确生命周期：在 Session 被销毁时清理映射条目，而非仅在 Agent 断开时清理。

```go
// relay.go 新增
func (r *Relay) CleanSession(sid string) {
    r.mu.Lock(); defer r.mu.Unlock()
    delete(r.sessionOwners, sid)
    delete(r.pendingInits, sid)
}
```

在 `connectrpc/handler.go` 的 recvLoop 中：
```go
if _, ok := msg.Payload.(*fishttyv1.TunnelMessage_SessionDestroyed); ok {
    h.relay.CleanSession(msg.SessionId)
}
h.relay.RouteFromAgent(deviceID, msg)
```

Relay SHALL 在以下场景主动向消息源端发送错误：
- `RouteFromMobile` 找不到目标 Agent → 发送 `ErrorMsg{code: AGENT_UNREACHABLE}`
- `channelSender.SendMessage` channel 满 → 发送 `ErrorMsg{code: CHANNEL_FULL}`

#### Scenario: Session 销毁后 sessionOwners 正确清理
- **WHEN** Agent 发送 `SessionDestroyed { session_id: "abc" }` 到 Server
- **THEN** Server 从 `sessionOwners` 中移除 "abc"
- **AND** Server 从 `pendingInits` 中移除 "abc"
- **AND** 后续 `fishtty_active_sessions` 指标正确反映实际活跃 session 数

#### Scenario: Agent 断开时批量清理
- **WHEN** Agent 连接断开触发 `UnregisterAgent(deviceID)`
- **THEN** Server 清理该 deviceID 的所有 `sessionOwners` 和 `pendingInits` 条目
- **AND** 行为与修改前一致（此路径不变）

### Requirement: fishtty-agent — Heartbeat
Agent 的心跳检测 SHALL 支持通过配置文件调整参数：

```yaml
heartbeat:
  interval: 15s        # 心跳发送间隔
  miss_threshold: 3     # 连续未收到 ACK 的次数阈值
```

默认值保持不变（15s 间隔，3 次阈值 = 45s 超时）。

#### Scenario: 心跳超时触发重连
- **WHEN** Agent 连续 3 次发送 Heartbeat 后未收到 HeartbeatAck
- **THEN** heartbeatLoop 退出
- **AND** 触发 Tunnel 重连流程
- **AND** `DestroyAll()` 被调用清理所有 session

### Requirement: fishtty-web — Terminal UI
Web 前端的终端渲染 SHALL 加载以下 xterm.js addons：

1. `@xterm/addon-webgl` — GPU 加速渲染（主渲染器）
2. `@xterm/addon-canvas` — Canvas 渲染器（WebGL 不可用时的回退）
3. `@xterm/addon-unicode11` — Unicode 11 列宽计算修正
4. `@xterm/addon-fit` — 自适应尺寸（已有，保持不变）

#### Scenario: WebGL 优先加载
- **WHEN** Terminal 组件初始化
- **THEN** 按顺序加载 FitAddon → WebglAddon（try）→ CanvasAddon（catch）→ Unicode11Addon
- **AND** WebGL context loss 时自动降级到 Canvas

### Requirement: fishtty-web — WebSocket Client
Web 前端的 WebSocket 客户端 SHALL：
- 在 `onerror` 时提取可用错误信息并通过 `onError` 回调通知
- 在连接建立阶段设置 10 秒超时
- 每 30 秒发送应用层 ping 文本帧
- 将 `activeSessions` 和 `deviceId` 持久化到 localStorage
- 重连后若 `activeSessions` 为空且 localStorage 有历史记录，自动创建新 Session

#### Scenario: WebSocket 错误报告
- **WHEN** WebSocket 连接触发 onerror 事件
- **THEN** 客户端通过 `onError` 回调传递包含 close code 和 reason 的 Error 对象
- **AND** UI 显示具体错误信息而非静默处理

## ADDED Requirements

### Requirement: Health Check 端点
Server SHALL 提供 `/health` HTTP 端点，返回 JSON 格式的健康状态。

#### Scenario: 健康检查正常
- **WHEN** `GET /health` 被请求
- **THEN** 返回 200 OK，body 为 `{"status":"ok","agents":1,"mobiles":1,"sessions":1}`
- **AND** 响应时间 <10ms

#### Scenario: Agent 全部离线
- **WHEN** `GET /health` 被请求且无 Agent 在线
- **THEN** 返回 503 Service Unavailable，body 包含 `"status":"degraded"`

### Requirement: WebSocket 读缓冲区增大
Server 端 WebSocket upgrader 的读写缓冲区 SHALL 从 4096 提升至 65536 字节。

#### Scenario: 大帧传输
- **WHEN** 终端输出大量数据（如 `cat largefile`）
- **THEN** 单个 WebSocket 帧可承载最多 64KB 数据
- **AND** 减少帧分片次数和系统调用
