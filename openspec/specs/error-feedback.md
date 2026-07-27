# fishtty Error Feedback

## Purpose

确保 fishtty 系统的每一层错误（WebSocket、会话、中继）都能转化为用户可见、可操作的反馈，消除静默黑屏问题。

## Requirements

### Requirement: WebSocket 错误可见
系统 SHALL 在 WebSocket 连接发生错误时，将具体错误信息以 Toast 形式展示给用户，而非静默处理。

#### Scenario: WebSocket 连接被拒绝
- **WHEN** WebSocket upgrade 请求返回非 101 状态码
- **THEN** 客户端显示 Toast "[连接错误] 服务器拒绝连接 (HTTP 4xx)"

#### Scenario: TLS 握手失败
- **WHEN** WebSocket 连接因 TLS 证书错误而失败
- **THEN** 客户端显示 Toast "[连接错误] TLS 握手失败，请检查服务器证书"

#### Scenario: 网络不可达
- **WHEN** WebSocket 连接在 10 秒内未收到任何响应
- **THEN** 客户端显示 Toast "[连接错误] 连接超时，请检查网络和服务器地址"

### Requirement: 连接超时检测
客户端 SHALL 在 WebSocket 连接建立阶段设置 10 秒超时，超时后自动关闭连接并通知用户。

#### Scenario: 连接超时后自动重试
- **WHEN** WebSocket 连接超时
- **THEN** 客户端等待退避延迟后自动重连，并保留错误提示 5 秒

### Requirement: Session 不存在时的错误反馈
当客户端发送 Reattach 或 DataChunk 到一个不存在的 Session 时，Agent 端 SHALL 返回 `ERROR_CODE_SESSION_LOST` 错误，客户端 SHALL 显示明确的操作指引。

#### Scenario: Reattach 到已销毁的 Session
- **WHEN** 客户端在重连后 Reattach 到已被销毁的 session_id
- **THEN** Agent 返回 `ErrorMsg { code: ERROR_CODE_SESSION_LOST, message: "会话已过期" }`
- **AND** 客户端显示 Toast "[会话丢失] 终端会话已过期，正在自动创建新会话"
- **AND** 客户端自动发起 SessionInit 创建新终端

#### Scenario: 输入数据到不存在的 Session
- **WHEN** 用户在已销毁的 Session 中输入字符
- **THEN** Agent 返回 `ErrorMsg { code: ERROR_CODE_SESSION_NOT_FOUND, message: "session 不存在" }`
- **AND** 客户端显示 Toast 并提示创建新会话

### Requirement: Agent 不可达时的错误反馈
当客户端消息无法路由到 Agent 时，Server 中继层 SHALL 主动向客户端发送错误消息。

#### Scenario: Agent 离线时发送 SessionInit
- **WHEN** 客户端发送 SessionInit 但目标 device_id 的 Agent 未连接
- **THEN** Server 返回 `ErrorMsg { code: ERROR_CODE_AGENT_UNREACHABLE, message: "目标设备不在线" }`
- **AND** 客户端显示 Toast "[设备离线] 目标设备未连接，请稍后重试"

### Requirement: 中继通道拥塞通知
当中继 channel 满导致消息被丢弃时，Server SHALL 向消息的源端发送背压错误。

#### Scenario: Mobile 通道满导致消息丢弃
- **WHEN** Mobile 的 drain channel（容量 256）已满且新消息被丢弃
- **THEN** Server 向发送方返回 `ErrorMsg { code: ERROR_CODE_CHANNEL_FULL, message: "通道拥塞，消息已丢弃" }`

### Requirement: 错误 Toast 管理
客户端 SHALL 以非阻塞 Toast 形式展示错误，最多同时显示 3 条，每条持续 5 秒后自动消失。不同错误码 SHALL 使用不同视觉样式（颜色/图标）。

#### Scenario: 多条错误同时到达
- **WHEN** 3 条以上错误同时触发
- **THEN** 客户端仅显示最近的 3 条，旧错误被替换而非堆积

### Requirement: WebSocket 关闭原因分类
服务端 SHALL 在 WebSocket 连接关闭时记录区分不同原因的日志，包含 close code 和错误详情。

#### Scenario: 客户端主动断开
- **WHEN** WebSocket 收到 close code 1000
- **THEN** 服务端记录 INFO 日志 "客户端主动断开"

#### Scenario: 网络异常断开
- **WHEN** WebSocket ReadMessage 返回非预期的关闭错误
- **THEN** 服务端记录 WARN 日志包含具体错误信息（而非仅 "WebSocket 已断开"）
