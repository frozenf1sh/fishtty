## MODIFIED Requirements

### Requirement: ErrorCode 枚举
`ErrorCode` 枚举 SHALL 包含以下完整错误码集合以覆盖全链路错误场景：

```protobuf
enum ErrorCode {
  ERROR_CODE_UNSPECIFIED = 0;
  ERROR_CODE_SESSION_NOT_FOUND = 1;
  ERROR_CODE_COMMAND_NOT_FOUND = 2;
  ERROR_CODE_COMMAND_FAILED = 3;
  ERROR_CODE_SESSION_LIMIT_REACHED = 4;
  ERROR_CODE_INVALID_MESSAGE = 5;
  ERROR_CODE_INTERNAL_ERROR = 6;
  ERROR_CODE_UNAUTHORIZED = 7;
  ERROR_CODE_SESSION_LOST = 8;         // 新增：session 已被销毁/过期
  ERROR_CODE_AGENT_UNREACHABLE = 9;    // 新增：目标 Agent 不在线
  ERROR_CODE_CHANNEL_FULL = 10;        // 新增：中继通道拥塞
  ERROR_CODE_CONNECTION_TIMEOUT = 11;  // 新增：连接超时
}
```

#### Scenario: 新错误码被旧客户端接收
- **WHEN** 新版本 Server 发送 `ERROR_CODE_SESSION_LOST` 到旧版本客户端
- **THEN** protobuf 反序列化将未知枚举值设为 0（`ERROR_CODE_UNSPECIFIED`）
- **AND** 客户端不崩溃

### Requirement: 错误处理表
错误处理表 SHALL 覆盖以下新增场景：

| Error Scenario | Agent Action | Server Action | Mobile Action |
|---------------|-------------|---------------|---------------|
| Session 已销毁（Reattach 到过期会话） | Send `Error{code: SESSION_LOST}` | Forward error to client | Show toast "会话已过期"，自动创建新 Session |
| Agent 不在线（Mobile 发 SessionInit 时） | N/A | Send `Error{code: AGENT_UNREACHABLE}` directly | Show toast "设备不在线" |
| Relay channel 满 | N/A | Send `Error{code: CHANNEL_FULL}` to source | Show toast "通道拥塞" |
| WebSocket 连接超时 | N/A | Close connection with 1013 code | Show toast "连接超时"，触发重连 |
| Session 销毁 | Send `SessionDestroyed`, signal relay to clean | Clean `sessionOwners` map entry | Remove session tab |

#### Scenario: SessionDestroyed 触发 Server 清理
- **WHEN** Agent 发送 `SessionDestroyed { session_id: "abc" }` 到 Server
- **THEN** Server 的 `recvLoop` 检测到 `SessionDestroyed` 消息类型
- **AND** 调用 `relay.CleanSession("abc")` 从 `sessionOwners` 和 `pendingInits` 中移除

### Requirement: Mobile Client WebSocket State Machine
Mobile 客户端 WebSocket 状态机 SHALL 增加以下转换规则：

#### Scenario: 连接超时转换
- **WHEN** WebSocket 处于 WS_CONNECTING 状态超过 10 秒
- **THEN** 状态转换为 WS_RECONNECTING
- **AND** 触发 `onError` 回调并显示连接超时 Toast

#### Scenario: 重连风暴保护
- **WHEN** 60 秒内 WS_RECONNECTING → WS_CONNECTING 转换超过 5 次
- **THEN** 退避延迟上限从 10 秒提升到 30 秒
- **AND** 显示持久错误提示

## ADDED Requirements

### Requirement: WebSocket 应用层 Ping/Pong
Mobile 客户端与 Server 之间的 WebSocket 连接 SHALL 使用应用层文本帧进行心跳检测。

#### Scenario: Ping/Pong 正常交互
- **WHEN** 客户端每 30 秒通过 WebSocket 发送文本帧 "ping"
- **THEN** 服务端立即回复文本帧 "pong"
- **AND** 这些文本帧不经过 protobuf 序列化，不进入 relay

#### Scenario: Pong 超时
- **WHEN** 客户端发送 "ping" 后 10 秒未收到 "pong"
- **THEN** 客户端视连接为断开并触发重连
