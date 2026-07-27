## ADDED Requirements

### Requirement: 应用层心跳检测
客户端 SHALL 每 30 秒通过 WebSocket 发送一个轻量级 ping 帧（纯文本，非 protobuf）。服务端 SHALL 在收到 ping 后立即回复 pong。客户端若 10 秒内未收到 pong 回应，SHALL 视为连接断开并触发重连。

#### Scenario: 心跳正常
- **WHEN** 客户端每 30 秒发送 "ping" 文本帧
- **AND** 服务端在 1 秒内回复 "pong" 文本帧
- **THEN** 连接状态保持 WS_ACTIVE
- **AND** 无用户可见提示

#### Scenario: 心跳超时触发重连
- **WHEN** 客户端发送 ping 后 10 秒未收到 pong
- **THEN** 客户端主动关闭 WebSocket 连接
- **AND** 状态变为 WS_RECONNECTING
- **AND** 显示 Toast "[连接中断] 网络心跳超时，正在重连..."

#### Scenario: 移动网络切换
- **WHEN** 设备从 WiFi 切换到蜂窝网络（TCP 连接假死但未断开）
- **THEN** 下一次 ping（≤30s 后）在 10 秒内未收到 pong
- **AND** 客户端检测到死连接并触发重连

### Requirement: 重连后自动恢复终端
客户端 SHALL 在 WebSocket 重连成功后，若本地 `activeSessions` 为空且之前曾有过活跃会话（从 localStorage 读取），则自动创建新的终端会话。

#### Scenario: PWA 重载后重连
- **WHEN** 用户刷新页面或 PWA 被系统杀死后重新打开
- **AND** WebSocket 连接到已注册的 device_id 成功
- **AND** localStorage 中有 `fishtty_last_active` 标记
- **THEN** 客户端自动发送 SessionInit 创建新终端
- **AND** 用户看到可用终端而非黑屏

#### Scenario: 正常首次连接
- **WHEN** 用户首次在设备列表页连接到一个设备
- **AND** localStorage 中无 `fishtty_last_active` 标记
- **THEN** 客户端切换到终端页但不自动创建 Session
- **AND** 用户可点击 "+ 终端" 手动创建

### Requirement: 重连退避保护
客户端 SHALL 在短时间内（60 秒内）重连超过 5 次时，将退避延迟上限从 10 秒提升到 30 秒，并显示持续错误提示。

#### Scenario: 重连风暴保护
- **WHEN** 60 秒内发生 5 次以上断开-重连循环
- **THEN** 退避延迟上限提升至 30 秒
- **AND** 显示持久 Toast "[连接不稳定] 频繁断连，已降低重连频率"
- **AND** 用户可手动点击按钮强制立即重连

### Requirement: 背压传播
当中继层因 channel 满而丢弃消息时，SHALL 向消息发送方返回错误。Mobile 端的背压错误 SHALL 显示为用户 Toast；Agent 端的背压错误 SHALL 记录为 ERROR 日志。

#### Scenario: Mobile 通道满
- **WHEN** Mobile 的 drain channel（容量 256）满
- **AND** 新消息尝试入队被丢弃
- **THEN** Server 通过对应 Mobile 连接发送 `ErrorMsg { code: ERROR_CODE_CHANNEL_FULL }`
- **AND** 客户端显示 Toast "[性能警告] 数据通道拥塞，部分输出已丢弃"

### Requirement: 连接状态持久化
客户端 SHALL 将 `activeSessions` 和连接目标的 `deviceId` 持久化到 localStorage，使页面重载后可恢复状态。

#### Scenario: 页面重载后状态恢复
- **WHEN** 用户刷新浏览器页面
- **AND** localStorage 中存在 `fishtty_device_id` 和 `fishtty_last_active`
- **THEN** 客户端自动重连到该 device_id
- **AND** 自动创建新 Session（符合 "重连后自动恢复终端" 要求）
