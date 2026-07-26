## ADDED Requirements

### Requirement: Unified Protobuf message schema
The system SHALL use a single Protobuf message type `TunnelMessage` as the envelope for all Agent↔Server and Mobile↔Server communication. The message SHALL use a `oneof` field to discriminate between control and data payload types. Every message SHALL carry a `session_id` field (empty for tunnel-level messages like `AuthRequest` and `Heartbeat`).

#### Scenario: Serializing a data chunk
- **WHEN** the Agent reads N bytes from a PTY master fd for session "abc123"
- **THEN** the Agent constructs `TunnelMessage{session_id: "abc123", payload: DataChunk{seq: 42, data: <N raw bytes>}}` and sends it on the bidirectional stream

#### Scenario: Serializing a control message
- **WHEN** the Server needs to resize session "abc123" to 120 columns by 40 rows
- **THEN** the Server constructs `TunnelMessage{session_id: "abc123", payload: Resize{cols: 120, rows: 40}}` and sends it on the bidirectional stream

#### Scenario: Deserializing an unknown message type
- **WHEN** a receiver encounters a `TunnelMessage` with a `oneof` variant it does not recognize
- **THEN** the receiver SHALL log a warning and skip the message without disrupting the stream

### Requirement: Message type definitions
The Protobuf schema SHALL define the following message types within the `TunnelMessage.oneof payload`:

- **AuthRequest**: `{device_id: string, token: string}` — Agent identifies itself
- **AuthResponse**: `{status: AuthStatus, message: string}` — Server accepts or rejects
- **SessionInit**: `{session_id: string, cols: uint32, rows: uint32, command: string, env: map<string,string>}` — Server requests PTY creation
- **SessionCreated**: `{session_id: string, status: SessionStatus}` — Agent confirms PTY creation
- **DataChunk**: `{session_id: string, seq: uint64, data: bytes}` — Bidirectional raw terminal I/O
- **Resize**: `{session_id: string, cols: uint32, rows: uint32}` — Server→Agent PTY resize
- **Heartbeat**: `{timestamp: int64}` — Agent→Server liveness ping
- **HeartbeatAck**: `{timestamp: int64}` — Server→Agent liveness pong
- **Reattach**: `{session_id: string, last_ack_seq: uint64}` — Server→Agent, request delta replay
- **ReattachData**: `{session_id: string, start_seq: uint64, chunks: repeated DataChunk}` — Agent→Server, replayed history
- **SessionDestroy**: `{session_id: string}` — Server→Agent, terminate session
- **SessionDestroyed**: `{session_id: string}` — Agent→Server, session terminated
- **Error**: `{session_id: string, code: ErrorCode, message: string}` — Bidirectional error reporting

#### Scenario: All message types are defined in a single .proto file
- **WHEN** a developer runs `buf generate` on the project's proto directory
- **THEN** Go types and Connect-RPC service stubs are generated for all message types and the `FishTTY.Tunnel` RPC

### Requirement: Binary wire format
All messages SHALL be serialized using Protobuf binary encoding (not JSON, not text format). The wire protocol for Agent↔Server SHALL be the Connect protocol with `Content-Type: application/connect+proto`. The wire protocol for Mobile↔Server SHALL be WebSocket binary frames containing serialized `TunnelMessage` bytes.

#### Scenario: Agent sends a message to Server
- **WHEN** the Agent calls `stream.Send(&TunnelMessage{...})` on a Connect-RPC bidirectional stream
- **THEN** the Connect-Go client serializes the message as binary Protobuf and transmits it in an HTTP request body with `Content-Type: application/connect+proto`

#### Scenario: Mobile client receives terminal data
- **WHEN** the Server sends a WebSocket binary frame to the mobile client
- **THEN** the client calls `TunnelMessage.decode(new Uint8Array(frameData))` and routes the decoded message to the appropriate session's xterm.js instance

### Requirement: Sequence number semantics
Every `DataChunk` from Agent→Server SHALL carry a `seq` field that is a monotonically increasing unsigned 64-bit integer, scoped per session. The sequence SHALL start at 1 for the first `DataChunk` sent after session creation. The mobile client SHALL track `last_ack_seq` per session, updated on each received `DataChunk`.

#### Scenario: First data chunk after session creation
- **WHEN** the Agent sends the first `DataChunk` for a newly created session
- **THEN** the `seq` field is set to 1

#### Scenario: Mobile client updates last_ack_seq
- **WHEN** the mobile client receives `DataChunk{session_id: "abc123", seq: 42, data: ...}`
- **THEN** the client updates its tracked `last_ack_seq` for session "abc123" to 42 after successfully writing the data to xterm.js

### Requirement: Connection state machine
The Agent-to-Server tunnel SHALL follow a defined state machine with states: DISCONNECTED, CONNECTING, ACTIVE, RECONNECT. State transitions SHALL be logged at INFO level. The mobile client-to-Server WebSocket SHALL follow a parallel state machine: WS_DISCONNECTED, WS_CONNECTING, WS_ACTIVE, WS_RECONNECTING.

#### Scenario: Agent transitions through full lifecycle
- **WHEN** the Agent process starts
- **THEN** it enters DISCONNECTED → dials → CONNECTING → sends AuthRequest → receives AuthResponse{OK} → ACTIVE → stream breaks → RECONNECT → backoff delay → CONNECTING

#### Scenario: Mobile client reconnects after backgrounding
- **WHEN** the user returns to the PWA after the WebSocket was disconnected
- **THEN** the client enters WS_RECONNECTING, dials the Server WebSocket endpoint, sends a `Reattach` message per active session, receives `ReattachData` with catch-up content, and enters WS_ACTIVE
