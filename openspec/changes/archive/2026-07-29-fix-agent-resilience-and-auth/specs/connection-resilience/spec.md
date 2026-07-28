## ADDED Requirements

### Requirement: 隧道 goroutine 异常退出触发完整重连
Agent 隧道中的 sendLoop、recvLoop 任一 goroutine 因 panic 退出时 SHALL 取消共享的 context，触发所有同级 goroutine 级联退出并进入 backoff 重连流程。

#### Scenario: sendLoop panic 后触发重连
- **WHEN** Agent 的 sendLoop goroutine 因 panic 退出
- **THEN** panic 被 recover 并记录 error 日志
- **AND** 共享 context 被 cancel
- **AND** recvLoop 和 heartbeatLoop 在 <-ctx.Done() 时退出
- **AND** connect() 返回错误，Run() 进入 backoff 重连

#### Scenario: recvLoop panic 后触发重连
- **WHEN** Agent 的 recvLoop goroutine 因 panic 退出
- **THEN** panic 被 recover 并记录 error 日志
- **AND** 共享 context 被 cancel
- **AND** sendLoop 和 heartbeatLoop 在 <-ctx.Done() 时退出
- **AND** connect() 返回错误，Run() 进入 backoff 重连

#### Scenario: 正常退出不触发 cancel
- **WHEN** Agent 收到 SIGTERM 信号
- **AND** parentCtx 被 cancel
- **THEN** sendLoop/recvLoop/heartbeatLoop 正常退出
- **AND** connect() 返回 context.Canceled（非错误）
- **AND** Run() 不进入重连循环，agent 优雅退出

## ADDED Requirements

### Requirement: 设备重注册时校验 token 一致性
Device Registry 在设备已存在且重新注册时 SHALL 校验新 token 与已存储 token 是否一致。不一致时 SHALL 拒绝注册并返回明确错误。

#### Scenario: token 匹配时更新设备信息
- **WHEN** agent 重连时携带与首次注册相同的 token
- **THEN** Register 返回成功
- **AND** agentVer、hostname、platform 更新为最新值

#### Scenario: token 不匹配时拒绝注册
- **WHEN** agent 重连时携带与首次注册不同的 token
- **THEN** Register 返回错误 "设备 xxx token 不匹配"
- **AND** 已存储的 token 不被修改
- **AND** agent 日志中可见明确错误信息

#### Scenario: 首次注册不受影响
- **WHEN** deviceID 在 registry 中不存在
- **THEN** 正常创建新设备记录
- **AND** token 按首次提供的值存储
