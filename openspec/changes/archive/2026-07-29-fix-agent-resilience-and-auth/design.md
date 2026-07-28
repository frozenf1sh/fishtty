## Context

当前两个问题：

**Token 静默忽略**：`DeviceRegistry.Register` 在设备已存在时仅更新 `agentVer/hostname/platform` 三个字段，不触碰 `token`。如果管理员在配置文件中更换了 token，agent 重连时 `Register` 返回成功（旧 token 不变），`Authenticate` 仍用旧 token 验证。用户无感知——他们以为 token 已更新，但实际凭据从未改变。

**半死隧道**：`TunnelService.sendLoop`/`recvLoop` 各自用 `defer recoverLog()` 做 panic 恢复。恢复后仅记录日志然后 goroutine 退出。但共享的 `ctx` 不被 cancel——因为 `cancel` 只在 `connect()` 的 `defer cancel()` 中调用，而 `connect()` 被 `ctx.Done()` 阻塞直到 `parentCtx` 取消。这意味着：
```
connect() 等待 ctx.Done()
  ├─ sendLoop 已退出（panic recover → return）
  ├─ recvLoop 可能还在跑 或 也已退出
  ├─ heartbeatLoop 继续发心跳
  └─ connect() 继续阻塞 ← ctx 没 cancel！
```
需等 heartbeatLoop 达到 `missThreshold` 才会 return 触发重连。在此之前漏斗：connection 看起来活跃但无法发送任何数据。

## Goals / Non-Goals

**Goals:**
- 设备重注册时 token 不匹配应返回明确错误
- 任一 tunnel goroutine 异常退出时立即触发完整重连
- 保持向后兼容：现有 agent/server 部署不受影响

**Non-Goals:**
- 不引入 token 轮换（rotation）机制
- 不改变 panic recovery 策略（仍 recover，不 crash）
- 不修改 protobuf 协议

## Decisions

### Decision 1: Token 校验而非静默更新

**选择**：在 `Register` 中检测 token 不匹配时返回 `fmt.Errorf("设备 %s token 不匹配", deviceID)`，拒绝注册。

**备选方案**：
- *静默更新 token*：有安全隐患——如果攻击者拿到一个 deviceID 就能覆盖 token 控制该设备。当前的"首次注册 token 永久有效"设计其实是一种简单的信任锚定。
- *要求旧 token 确认*：像密码修改一样要求先验证旧 token。过度设计，目前没有安全需求支持。

**原理**：拒绝 + 明确错误让运维人员立刻发现问题（日志中会看到 "设备 xxx token 不匹配"），而非静默失败。首次注册行为不变（设备不存在时正常创建）。

### Decision 2: cancel context 触发重连

**选择**：在 sendLoop 和 recvLoop 的 panic recover 中调用 `cancel()`。

```
sendLoop panic:
  recover → log → cancel() → ctx.Done() → connect() 中 wg.Wait() 解除
  → cancel() 传播到 recvLoop/heartbeatLoop → 全部退出
  → connect() 返回 error → Run() 中 backoff + reconnect
```

**备选方案**：
- *在 connect() 中用 select 监听 goroutine 退出 channel*：需要额外的 done channel 和 select 逻辑，侵入性更大。
- *不做 panic recover，让进程 crash*：Go 标准库推荐的做法，但对生产环境不友好——systemd 重启 agent 有延迟，且会丢失所有活跃 session。

**原理**：`cancel()` 是 Go context 的标准取消传播机制，所有 goroutine 的 `select { case <-ctx.Done() }` 会立即触发，干净的级联退出。backoff 重连流程保持不变。

## Risks / Trade-offs

- **[Risk] Token 拒绝可能导致 agent 陷入重连循环** → **Mitigation**：`Run()` 的重连循环会持续重试，但 `Register` 的 token 错误是持久性错误（非瞬态），应该在几次重试后停止。当前 backoff 从 1s 到 60s，agent 会反复重试——用户看到日志会明白需要修正配置。之后可考虑对 `AUTH_STATUS_UNAUTHORIZED` 类型的错误做特殊处理（停止重连），但不在此变更范围内。
- **[Risk] cancel 后 tunnel 中尚未发送的消息丢失** → **Mitigation**：与现有行为一致——当前 panic 恢复后 sendCh 中的消息同样丢失，因为 sendLoop 已退出。cancel 触发完整重连后，ring buffer replay 能恢复 PTY 输出。
