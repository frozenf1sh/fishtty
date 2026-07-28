## Why

两个 Agent/Server 稳定性缺陷：(1) Device Registry 在设备重新注册时静默忽略新 token，导致用户更改 token 后仍用旧凭据认证——安全混淆且行为不符合预期；(2) Agent 的 sendLoop/recvLoop goroutine panic 恢复后不触发上下文取消，导致隧道半死：sendLoop 退出后 recvLoop 和 heartbeatLoop 继续运行，前端看到心跳正常但所有输入静默丢失。

## What Changes

- Device Registry 的 `Register` 方法在设备已存在时增加 token 校验：新 token 与旧 token 不匹配则拒绝注册并返回明确错误
- Agent 的 `sendLoop` 和 `recvLoop` goroutine 任一个异常退出时 cancel 共享的 context，触发完整的隧道重连流程
- 用统一的 context 取消替代各自独立的 panic recover-and-return

## Capabilities

### New Capabilities
<!-- None — these are bug fixes to existing behavior -->

### Modified Capabilities
- `connection-resilience`: 新增 Agent 隧道 goroutine 级容错需求——任一流 loop 异常退出 SHALL 触发完整重连，不得留下半死隧道。修改设备认证需求——重新注册时 token 不匹配 SHALL 拒绝而非静默忽略。

## Impact

- `internal/server/service/registry.go`：`Register()` 增加 token 匹配校验
- `internal/agent/service/tunnel.go`：`sendLoop`/`recvLoop` panic 恢复后 cancel context
- 无协议变更，无 API 变更，无新增依赖
