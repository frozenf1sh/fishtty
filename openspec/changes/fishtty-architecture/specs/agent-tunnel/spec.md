## ADDED Requirements

### Requirement: Agent establishes reverse tunnel to Server
The Agent SHALL initiate a single outbound Connect-RPC bidirectional stream to the Server on startup and maintain it for the lifetime of the process. The Agent SHALL be the TCP connection initiator (the Connect-RPC client); the Server SHALL never dial the Agent.

#### Scenario: Agent starts and connects to Server
- **WHEN** the Agent process starts with a configured Server address and device token
- **THEN** the Agent opens a TLS connection to the Server, sends an `AuthRequest` message as the first message on the bidirectional stream, and transitions to CONNECTING state

#### Scenario: Agent receives AuthResponse success
- **WHEN** the Agent receives `AuthResponse{status: OK}` from the Server
- **THEN** the Agent transitions to ACTIVE state and begins sending periodic `Heartbeat` messages every 15 seconds

#### Scenario: Authentication fails
- **WHEN** the Agent receives `AuthResponse{status: UNAUTHORIZED}` from the Server
- **THEN** the Agent closes the connection, logs the failure, and enters RECONNECT state with exponential backoff starting at 1 second

### Requirement: Agent heartbeat and connection health
The Agent SHALL send a `Heartbeat` message every 15 seconds while in ACTIVE state. The Server SHALL respond with `HeartbeatAck` within 5 seconds. If three consecutive heartbeats are unacknowledged, the Agent SHALL consider the connection dead and enter RECONNECT state.

#### Scenario: Normal heartbeat exchange
- **WHEN** the Agent is in ACTIVE state and 15 seconds have elapsed since the last heartbeat
- **THEN** the Agent sends `Heartbeat{timestamp}` and resets its heartbeat timer

#### Scenario: Heartbeat timeout
- **WHEN** the Agent has sent three consecutive `Heartbeat` messages without receiving a `HeartbeatAck`
- **THEN** the Agent closes the stream, logs a connection timeout, and enters RECONNECT state with exponential backoff

### Requirement: Agent reconnection with exponential backoff
The Agent SHALL retry connection attempts with exponential backoff: 1s, 2s, 4s, 8s, 16s, 32s, to a maximum of 60s between attempts. The backoff SHALL reset to 1s after a successful connection lasting at least 30 seconds.

#### Scenario: First reconnection attempt
- **WHEN** the Agent enters RECONNECT state for the first time after a disconnect
- **THEN** the Agent waits 1 second before attempting to dial the Server

#### Scenario: Repeated connection failures
- **WHEN** the Agent has failed to connect 5 consecutive times
- **THEN** the Agent waits 32 seconds before the 6th attempt

#### Scenario: Backoff reset after stable connection
- **WHEN** the Agent has been in ACTIVE state for at least 30 seconds and then disconnects
- **THEN** the next reconnection delay starts at 1 second

### Requirement: PTY session creation via tunnel
The Server SHALL send a `SessionInit` message over the tunnel to request creation of a new PTY session. The Agent SHALL create a PTY using `creack/pty`, start the requested command (default: user's shell), and respond with `SessionCreated` containing the assigned `session_id`.

#### Scenario: Server requests a new shell session
- **WHEN** the Server sends `SessionInit{cols: 80, rows: 24, command: "/bin/bash"}`
- **THEN** the Agent creates a PTY, starts `/bin/bash`, assigns a unique `session_id`, and responds with `SessionCreated{session_id, status: OK}`

#### Scenario: Server requests session with invalid command
- **WHEN** the Server sends `SessionInit{command: "/nonexistent/binary"}`
- **THEN** the Agent responds with `Error{session_id: "", code: COMMAND_NOT_FOUND, message: "..."}`

### Requirement: Agent session destruction
The Agent SHALL terminate the PTY process, close the PTY master file descriptor, release the ring buffer, and free all associated resources when it receives a `SessionDestroy` message for a valid `session_id`. The Agent SHALL respond with a final `SessionDestroyed` acknowledgment.

#### Scenario: Server destroys an active session
- **WHEN** the Server sends `SessionDestroy{session_id: "abc123"}`
- **THEN** the Agent sends SIGHUP to the PTY process group, waits up to 2 seconds for graceful exit, force-kills with SIGKILL if necessary, closes the PTY fd, releases the ring buffer, and sends `SessionDestroyed{session_id: "abc123"}`

#### Scenario: Destroy request for unknown session
- **WHEN** the Server sends `SessionDestroy{session_id: "nonexistent"}`
- **THEN** the Agent responds with `Error{session_id: "nonexistent", code: SESSION_NOT_FOUND}`
