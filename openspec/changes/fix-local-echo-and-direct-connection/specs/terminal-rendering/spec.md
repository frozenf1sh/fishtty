## MODIFIED Requirements

### Requirement: 交替缓冲区感知的本地回显
本地回显（EchoBuffer）SHALL 使用**本地序号追踪**替代逐字符前缀匹配。每次用户输入递增本地序号 `echoSeq`，发送 DataChunk 时携带 `echo_seq`。服务端原样回传 `echo_seq`。drain 时按序号范围确认本地写入已被服务端消费，剥离对应前缀后返回剩余数据。

在交替缓冲区模式下（`inAltBuffer = true`），序号追踪暂停，直接透传服务端数据。

#### Scenario: 低延迟下无双击键（核心场景）
- **WHEN** RTT < 10ms
- **AND** 用户输入 'l'
- **AND** 服务端回显在 rAF flush 发送输入之前到达
- **THEN** drain 通过 echo_seq 确认 'l' 已被本地写入
- **AND** 回显数据对应的前缀被剥离
- **AND** 终端不出现重复的 'l'

#### Scenario: 高延迟下正常回显
- **WHEN** RTT > 100ms
- **AND** 用户输入 'echo hello'
- **AND** 服务端在发送完成数毫秒后回显 "echo hello\r\n"
- **THEN** drain 通过 echo_seq 匹配对应的 pending 条目
- **AND** 回显前缀被正确剥离
- **AND** 只有 `\r\n` 被写入终端

#### Scenario: vim 进入交替缓冲区
- **WHEN** 终端接收并解析到 `\x1b[?1049h` 序列
- **THEN** EchoBuffer 清空 pending 序号映射
- **AND** 标记状态为 `inAltBuffer = true`
- **AND** 后续终端输出由 drain 直接透传（不做序号匹配）

#### Scenario: vim 退出交替缓冲区
- **WHEN** 终端接收并解析到 `\x1b[?1049l` 序列
- **THEN** EchoBuffer 标记状态为 `inAltBuffer = false`
- **AND** echoSeq 重置为 0
- **AND** 后续用户输入恢复序号追踪模式

#### Scenario: 正常模式下输入不受影响
- **WHEN** 终端不在交替缓冲区内（`inAltBuffer = false`）
- **THEN** EchoBuffer 使用序号追踪模式：本地写入 + 序号匹配剥离

## MODIFIED wire format

### Requirement: DataChunk 协议扩展
`DataChunk` 消息 SHALL 新增 `echo_seq` 字段，用于 Mobile→Server 时携带本地回显序号，Agent→Server 回传时原样保持。

```protobuf
message DataChunk {
  string session_id = 1;
  uint64 seq = 2;         // PTY 输出序号（Agent→Server，单调递增）
  bytes data = 3;         // 原始终端字节
  uint32 echo_seq = 4;    // 本地回显序号（0 = 未启用序号追踪）
}
```

#### Scenario: 新客户端发送 echo_seq
- **WHEN** 新版 PWA 发送 DataChunk
- **THEN** `echo_seq` 为非零值，表示发送时的本地序号
- **AND** Agent readLoop 将 `echo_seq` 原样写入回显 DataChunk

#### Scenario: 旧客户端兼容
- **WHEN** 旧版客户端发送 DataChunk 且不包含 `echo_seq`
- **THEN** proto3 默认值为 0
- **AND** 服务端/Agent 按 `echo_seq=0` 处理（等于未启用序号追踪）

#### Scenario: 序号溢出回绕
- **WHEN** `echoSeq` 在长时间会话中达到 `uint32` 上限后回绕
- **THEN** 序号从 0 重新开始
- **AND** 旧 pending 条目在回绕时清空，确保不匹配上一轮的同序号数据
