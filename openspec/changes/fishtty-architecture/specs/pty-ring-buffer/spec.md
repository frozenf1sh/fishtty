## ADDED Requirements

### Requirement: Per-session in-memory ring buffer
The Agent SHALL allocate a 128 KB circular byte buffer for each PTY session at session creation time. The buffer SHALL store all output read from the PTY master fd, interleaved with sequence-number headers, in a circular (overwrite-oldest) fashion.

#### Scenario: Ring buffer allocation
- **WHEN** the Agent creates a new PTY session
- **THEN** a 128 KB (131,072 byte) ring buffer is allocated and associated with the session

#### Scenario: PTY output written to ring buffer
- **WHEN** the Agent reads N bytes from the PTY master fd
- **THEN** the bytes are written to the ring buffer with a header containing the current sequence number and length, the sequence number is incremented, and a `DataChunk{seq, data}` is sent to the Server

#### Scenario: Ring buffer wraps around
- **WHEN** writing a new chunk would exceed the remaining capacity of the ring buffer
- **THEN** the write wraps to the beginning of the buffer, overwriting the oldest chunks, and the `oldest_available_seq` is updated to the sequence number of the new oldest chunk

### Requirement: Sequence-numbered chunk storage
Each chunk in the ring buffer SHALL be prefixed with an 8-byte sequence number (uint64, big-endian) and a 4-byte length field (uint32, big-endian), followed by the raw PTY output bytes. The total per-chunk overhead SHALL be exactly 12 bytes.

#### Scenario: Chunk header encoding
- **WHEN** a chunk with seq=42 and 1024 bytes of PTY output is written to the ring buffer
- **THEN** the ring buffer stores: `[0x00 0x00 0x00 0x00 0x00 0x00 0x00 0x2A][0x00 0x00 0x04 0x00][1024 bytes of raw data]`

#### Scenario: Chunk header decoding
- **WHEN** the Agent needs to replay chunks starting from seq=N
- **THEN** it scans the ring buffer from the oldest chunk, reads each header to find the sequence number and length, and collects chunks with seq >= N

### Requirement: Reattach with delta replay
When the Agent receives a `Reattach{session_id, last_ack_seq}` message for a valid, active session, it SHALL replay all ring buffer chunks with `seq > last_ack_seq` by sending them as `ReattachData` messages. After replay completes, the Agent SHALL resume sending live `DataChunk` messages. The `ReattachData` message SHALL include `start_seq` to indicate the actual starting sequence number (which may be greater than `last_ack_seq + 1` if buffer overwrite occurred).

#### Scenario: Full catch-up from ring buffer
- **WHEN** the Agent receives `Reattach{session_id: "abc123", last_ack_seq: 40}` and the ring buffer contains chunks with seq 35-50
- **THEN** the Agent sends `ReattachData{session_id: "abc123", start_seq: 41, chunks: [chunks with seq 41-50]}` and then resumes live streaming from seq 51 onward

#### Scenario: Partial catch-up due to buffer overwrite
- **WHEN** the Agent receives `Reattach{session_id: "abc123", last_ack_seq: 10}` but the ring buffer's oldest chunk is seq 35
- **THEN** the Agent sends `ReattachData{session_id: "abc123", start_seq: 35, chunks: [chunks with seq 35-50]}` — the client receives `start_seq: 35` and knows that seq 11-34 are permanently lost

#### Scenario: Reattach to a session with no new data
- **WHEN** the Agent receives `Reattach{session_id: "abc123", last_ack_seq: 50}` and the ring buffer's newest chunk is seq 50
- **THEN** the Agent sends `ReattachData{session_id: "abc123", start_seq: 50, chunks: []}` and resumes live streaming

#### Scenario: Reattach to a destroyed session
- **WHEN** the Agent receives `Reattach{session_id: "abc123"}` but session "abc123" has been destroyed
- **THEN** the Agent responds with `Error{session_id: "abc123", code: SESSION_NOT_FOUND}`

### Requirement: Ring buffer memory management
The ring buffer SHALL be freed when the session is destroyed. The Agent SHALL NOT allocate a ring buffer larger than 128 KB per session without configuration. The total memory across all sessions SHALL be monitored and exposed as a metric.

#### Scenario: Memory released on session destroy
- **WHEN** a session is destroyed (by `SessionDestroy` or PTY process exit)
- **THEN** the 128 KB ring buffer is freed, the session struct is deallocated, and any goroutines serving the session exit

#### Scenario: Multiple sessions memory bound
- **WHEN** the Agent has 10 active PTY sessions
- **THEN** total ring buffer memory consumption SHALL NOT exceed 1.28 MB (10 × 128 KB)

### Requirement: Sequence number is durable within session lifetime
The sequence number counter SHALL persist for the entire lifetime of a PTY session. It SHALL NOT reset on Agent reconnection to Server. It SHALL reset to 1 only when a new PTY session is created.

#### Scenario: Sequence numbers continue across tunnel reconnections
- **WHEN** the Agent disconnects from the Server and reconnects while session "abc123" is still running
- **THEN** the next `DataChunk` for session "abc123" carries a sequence number that is one greater than the last chunk sent before disconnection
