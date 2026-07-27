# fishtty Protocol Specification

## Protobuf Schema

### Service Definition

```protobuf
syntax = "proto3";

package fishtty.v1;

option go_package = "github.com/frozenf1sh/fishpts/gen/fishtty/v1;fishttyv1";

// FishTTY is the bidirectional tunnel service.
// The Agent (behind NAT) calls Tunnel() to establish the reverse connection.
// The Server never dials the Agent.
service FishTTY {
  // Tunnel is a single bidirectional stream that carries all control and data
  // messages multiplexed by session_id and discriminated by the oneof payload.
  rpc Tunnel(stream TunnelMessage) returns (stream TunnelMessage);
}
```

### Message Envelope

```protobuf
message TunnelMessage {
  // session_id identifies the PTY session this message belongs to.
  // Empty for tunnel-level messages (AuthRequest, AuthResponse, Heartbeat, HeartbeatAck).
  string session_id = 1;

  // The message payload, discriminated by type.
  oneof payload {
    // ── Authentication ──
    AuthRequest    auth_req     = 10;
    AuthResponse   auth_resp    = 11;

    // ── Session Lifecycle ──
    SessionInit    session_init      = 20;
    SessionCreated session_created   = 21;
    SessionDestroy session_destroy   = 22;
    SessionDestroyed session_destroyed = 23;

    // ── Data Plane ──
    DataChunk      data_chunk   = 30;

    // ── Session Control ──
    Resize         resize       = 40;

    // ── Reconnection ──
    Reattach       reattach     = 50;
    ReattachData   reattach_data = 51;

    // ── Health ──
    Heartbeat      heartbeat    = 60;
    HeartbeatAck   heartbeat_ack = 61;

    // ── Errors ──
    ErrorMsg       error_msg    = 70;
  }
}
```

### Authentication Messages

```protobuf
message AuthRequest {
  // Unique device identifier (UUID, provisioned at agent install time).
  string device_id = 1;
  // Pre-shared authentication token.
  string token = 2;
  // Agent version string (for compatibility checks).
  string agent_version = 3;
  // Hostname of the machine running the agent.
  string hostname = 4;
  // OS and architecture (e.g., "linux/amd64").
  string platform = 5;
}

message AuthResponse {
  AuthStatus status = 1;
  // Human-readable message (especially useful on failure).
  string message = 2;
  // Server-assigned session token for the tunnel (valid for the lifetime of the connection).
  string tunnel_id = 3;
}

enum AuthStatus {
  AUTH_STATUS_UNSPECIFIED = 0;
  AUTH_STATUS_OK = 1;
  AUTH_STATUS_UNAUTHORIZED = 2;
  AUTH_STATUS_DEVICE_NOT_FOUND = 3;
  AUTH_STATUS_VERSION_TOO_OLD = 4;
}
```

### Session Lifecycle Messages

```protobuf
message SessionInit {
  // session_id is empty when sent by Server; Agent assigns and returns it.
  string session_id = 1;

  // Initial terminal dimensions in characters.
  uint32 cols = 2;
  uint32 rows = 3;

  // Command to execute. Empty string means the user's default shell.
  // Must be an absolute path or a single binary name in PATH.
  string command = 4;

  // Environment variables to set (merged with agent's environment).
  map<string, string> env = 5;

  // Working directory. Empty means the user's home directory.
  string work_dir = 6;
}

message SessionCreated {
  // Agent-assigned unique session identifier.
  string session_id = 1;
  SessionStatus status = 2;
  string message = 3;
}

enum SessionStatus {
  SESSION_STATUS_UNSPECIFIED = 0;
  SESSION_STATUS_OK = 1;
  SESSION_STATUS_FAILED = 2;
}

message SessionDestroy {
  string session_id = 1;
}

message SessionDestroyed {
  string session_id = 1;
}
```

### Data Plane Messages

```protobuf
message DataChunk {
  // PTY session identifier.
  string session_id = 1;

  // Sequence number. Monotonically increasing per session.
  // Starts at 1 for the first chunk after session creation.
  // Agent→Server chunks carry seq. Mobile→Server (stdin) chunks carry seq=0.
  uint64 seq = 2;

  // Raw terminal bytes (stdout from PTY, or stdin to PTY).
  bytes data = 3;
}
```

### Control Messages

```protobuf
message Resize {
  string session_id = 1;
  // Terminal width in character columns.
  uint32 cols = 2;
  // Terminal height in character rows.
  uint32 rows = 3;
}
```

### Reconnection Messages

```protobuf
message Reattach {
  // Session to reattach to.
  string session_id = 1;

  // Last sequence number acknowledged by the client.
  // Agent replays all ring buffer chunks with seq > last_ack_seq.
  uint64 last_ack_seq = 2;
}

message ReattachData {
  // Session identifier.
  string session_id = 1;

  // Actual starting sequence number of the first chunk in this message.
  // May be greater than (last_ack_seq + 1) if ring buffer overwrote older data.
  uint64 start_seq = 2;

  // Replayed chunks in sequence order.
  repeated DataChunk chunks = 3;
}
```

### Health Check Messages

```protobuf
message Heartbeat {
  // Unix timestamp in milliseconds.
  int64 timestamp = 1;
}

message HeartbeatAck {
  // Echo of the timestamp from the Heartbeat message.
  int64 timestamp = 1;
}
```

### Error Messages

```protobuf
message ErrorMsg {
  // Session identifier, empty for tunnel-level errors.
  string session_id = 1;

  // Machine-readable error code.
  ErrorCode code = 2;

  // Human-readable error description.
  string message = 3;
}

enum ErrorCode {
  ERROR_CODE_UNSPECIFIED = 0;
  ERROR_CODE_SESSION_NOT_FOUND = 1;
  ERROR_CODE_COMMAND_NOT_FOUND = 2;
  ERROR_CODE_COMMAND_FAILED = 3;
  ERROR_CODE_SESSION_LIMIT_REACHED = 4;
  ERROR_CODE_INVALID_MESSAGE = 5;
  ERROR_CODE_INTERNAL_ERROR = 6;
  ERROR_CODE_UNAUTHORIZED = 7;
  ERROR_CODE_SESSION_LOST = 8;         // session 已被销毁/过期
  ERROR_CODE_AGENT_UNREACHABLE = 9;    // 目标 Agent 不在线
  ERROR_CODE_CHANNEL_FULL = 10;        // 中继通道拥塞
  ERROR_CODE_CONNECTION_TIMEOUT = 11;  // 连接超时
}
```

## Connection State Machine

### Agent State Machine

```
                        ┌──────────────┐
         start ────────▶│ DISCONNECTED │
                        └──────┬───────┘
                               │ dial() succeeds
                               ▼
                        ┌──────────────┐
               ┌───────│  CONNECTING  │───────┐
               │       └──────┬───────┘       │
               │              │ Auth fails    │
               │              ▼               │
               │       ┌──────────────┐       │
               │       │    ACTIVE     │       │
               │       └──────┬───────┘       │
               │              │               │
               │    ┌─────────┼─────────┐     │
               │    │         │         │     │
               │    ▼         ▼         ▼     │
               │  Stream   Heartbeat   Error  │
               │  Error    Timeout            │
               │    │         │         │     │
               │    └─────────┼─────────┘     │
               │              ▼               │
               │       ┌──────────────┐       │
               └──────▶│  RECONNECT   │◄──────┘
                       │  (backoff)   │
                       └──────────────┘
```

| State | Description |
|-------|-------------|
| **DISCONNECTED** | No active TCP connection. Agent periodically attempts dial. |
| **CONNECTING** | TLS handshake complete. `AuthRequest` sent, awaiting `AuthResponse`. |
| **ACTIVE** | Authenticated. Bidirectional stream open. PTY sessions active. Heartbeats every 15s. |
| **RECONNECT** | Stream broken. Backoff timer active (1s→2s→4s→...→60s max). PTY sessions continue running. |

### State Transition Rules

1. **DISCONNECTED → CONNECTING**: Agent dials Server and completes TLS handshake.
2. **CONNECTING → ACTIVE**: `AuthResponse{status: OK}` received.
3. **CONNECTING → RECONNECT**: `AuthResponse{status: !OK}` received, or connection drops before auth completes.
4. **ACTIVE → RECONNECT**: Stream `Recv()` returns error, or 3 consecutive heartbeats unacknowledged.
5. **RECONNECT → CONNECTING**: Backoff timer expires. Agent dials Server.
6. **RECONNECT backoff reset**: Backoff resets to 1s after any ACTIVE period lasting ≥ 30 seconds.

### Mobile Client WebSocket State Machine

```
                    ┌─────────────────┐
     start ────────▶│  WS_DISCONNECTED │
                    └────────┬────────┘
                             │ new WebSocket(url)
                             ▼
                    ┌─────────────────┐
           ┌───────│  WS_CONNECTING   │───────┐
           │       └────────┬────────┘       │
           │                │ onopen         │
           │                ▼                │
           │       ┌─────────────────┐       │
           │       │   WS_ACTIVE     │       │
           │       └────────┬────────┘       │
           │                │                │
           │                │ onclose/error  │
           │                ▼                │
           │       ┌─────────────────┐       │
           └──────▶│ WS_RECONNECTING │◄──────┘
                   │ (backoff 1-10s) │
                   └─────────────────┘
```

On `WS_RECONNECTING → WS_ACTIVE` transition: for each active session, send `Reattach{session_id, last_ack_seq}`, process `ReattachData` response, then resume live `DataChunk` handling.

## WebSocket 应用层 Ping/Pong

Mobile 客户端与 Server 之间通过 WebSocket 文本帧进行应用层心跳检测：

- 客户端每 30 秒发送文本帧 `"ping"`
- 服务端立即回复文本帧 `"pong"`
- 这些文本帧不经过 protobuf 序列化，不进入 relay
- 客户端 10 秒未收到 pong → 视为连接断开 → 触发重连

## Mobile Client State Machine Extensions

Mobile WebSocket 状态机新增以下转换规则：

- **连接超时**: WS_CONNECTING 超过 10s → WS_RECONNECTING + 显示超时 Toast
- **Pong 超时**: WS_ACTIVE 中 10s 未收到 pong → 主动关闭 → WS_RECONNECTING
- **重连风暴保护**: 60s 内 >5 次 WS_RECONNECTING → WS_CONNECTING 转换 → 退避上限提升至 30s + 持久错误提示

## Wire Format Details

### Agent ↔ Server (Connect-RPC)

- Protocol: Connect protocol over HTTP/1.1 or HTTP/2 with TLS
- Content-Type: `application/connect+proto`
- Message framing: Connect protocol envelope (1-byte flags + 4-byte length prefix + Protobuf message)
- Unary messages are not used; all communication is on the bidirectional stream

### Mobile ↔ Server (WebSocket)

- Protocol: WebSocket over TLS (wss://)
- Sub-protocol: `fish-tty-v1` (negotiated during WebSocket upgrade)
- Message type: Binary frames only
- Each binary frame contains a single serialized `TunnelMessage`
- No JSON framing — the Protobuf binary encoding is used directly

## Sequence Number Protocol

```
Session "abc123" sequence number lifecycle:

   SessionCreated
        │
        ▼
   DataChunk{seq: 1}  ← first PTY output
   DataChunk{seq: 2}
   DataChunk{seq: 3}
        ...
   DataChunk{seq: N}  ← latest output
        │
        │ (mobile disconnects at seq=42)
        │ (PTY continues running, ring buffer accumulates)
        │
        ▼
   DataChunk{seq: N+1}
   DataChunk{seq: N+2}
        ...
        │
        │ (mobile reconnects)
        │
        ▼
   Reattach{session_id: "abc123", last_ack_seq: 42}
        │
        ▼
   ReattachData{start_seq: 43, chunks: [43, 44, ..., N+2]}
   DataChunk{seq: N+3}  ← back to live streaming
```

Rules:
- `seq` is per-session, starts at 1, increments by 1 for each `DataChunk` from Agent.
- Mobile→Server `DataChunk` (stdin) has `seq = 0` (not tracked).
- Sequence numbers are **not** reset on Agent tunnel reconnection.
- `uint64` wrapping is acceptable (after ~1.8×10^19 chunks).

## Error Handling

| Error Scenario | Agent Action | Server Action | Mobile Action |
|---------------|-------------|---------------|---------------|
| Invalid message (deserialization failure) | Log warning, skip message | Log warning, skip message | Log warning, skip message |
| Session not found | Send `Error{code: SESSION_NOT_FOUND}` | Forward error to client | Show toast, remove session tab |
| Session 已销毁（Reattach 到过期会话） | Send `Error{code: SESSION_LOST}` | Forward error to client | Show toast "会话已过期"，自动创建新 Session |
| Agent 不在线（Mobile 发 SessionInit） | N/A | Send `Error{code: AGENT_UNREACHABLE}` directly | Show toast "设备不在线" |
| Relay channel 满 | N/A | Send `Error{code: CHANNEL_FULL}` to source | Show toast "通道拥塞" |
| WebSocket 连接超时 | N/A | Close connection with 1013 code | Show toast "连接超时"，触发重连 |
| Session 销毁 | Send `SessionDestroyed`, signal relay to clean | Clean `sessionOwners` map entry | Remove session tab |
| Agent stream broken | Enter RECONNECT | Clean up agent connection; buffer mobile messages (limited) | Show "Reconnecting..." overlay |
| Mobile WebSocket broken | (Unaware — stream to Server continues) | Buffer or drop depending on session TTL | Show "Reconnecting...", attempt reattach |
| Ring buffer overflow (seq gap) | Continue normally | Forward `ReattachData` with `start_seq > last_ack_seq+1` | Display gap indicator, continue from `start_seq` |

## Field Number Allocation

Field numbers in the `TunnelMessage` oneof are grouped by category to allow future extension:

| Range | Category |
|-------|----------|
| 10-19 | Authentication |
| 20-29 | Session lifecycle |
| 30-39 | Data plane |
| 40-49 | Session control |
| 50-59 | Reconnection |
| 60-69 | Health checking |
| 70-79 | Error reporting |
| 80-99 | Reserved for future use |
