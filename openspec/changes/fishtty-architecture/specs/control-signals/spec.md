## ADDED Requirements

### Requirement: Resize throttle at 50 ms
The mobile client SHALL debounce terminal resize events by 50 ms before sending a `Resize` message to the Server. Only the final dimensions after the debounce window SHALL be transmitted. The Server SHALL forward the `Resize` message to the Agent without additional throttling.

#### Scenario: Single resize event
- **WHEN** the terminal container size changes once (e.g., small window resize)
- **THEN** the client waits 50 ms, and if no further resize occurs, sends one `Resize{cols, rows}` message

#### Scenario: Rapid burst of resize events
- **WHEN** the terminal container size changes 20 times within 200 ms (e.g., keyboard animation)
- **THEN** the client sends exactly one `Resize` message with the final dimensions, 50 ms after the last resize event

#### Scenario: Continuous resize (drag)
- **WHEN** the user drags a split pane divider, producing continuous resize events over 2 seconds
- **THEN** the client sends a `Resize` message at most every 50 ms during the drag

### Requirement: PTY size synchronization
Upon receiving a `Resize` message, the Agent SHALL call `pty.Setsize()` on the corresponding PTY master file descriptor with the specified rows and columns. The Agent SHALL set pixel dimensions as `X = Cols * 8` and `Y = Rows * 16`.

#### Scenario: Agent applies resize
- **WHEN** the Agent receives `Resize{session_id: "abc123", cols: 120, rows: 40}`
- **THEN** the Agent calls `pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120, X: 960, Y: 640})`, causing the kernel to send SIGWINCH to the foreground process group

#### Scenario: Resize for non-existent session
- **WHEN** the Agent receives `Resize{session_id: "nonexistent", ...}`
- **THEN** the Agent responds with `Error{session_id: "nonexistent", code: SESSION_NOT_FOUND}`

### Requirement: Control character injection
The Agent SHALL handle incoming `DataChunk` messages by writing the raw `data` bytes directly to the corresponding PTY master file descriptor. No interpretation, filtering, or transformation of the bytes SHALL occur on the Agent. Control characters (e.g., `\x03` for SIGINT) are passed through and handled by the PTY's terminal line discipline.

#### Scenario: Ordinary text input
- **WHEN** the Agent receives `DataChunk{data: [0x68, 0x65, 0x6C, 0x6C, 0x6F, 0x0D]}` ("hello\r")
- **THEN** the Agent writes exactly those 6 bytes to the PTY master fd

#### Scenario: Control-C injection
- **WHEN** the Agent receives `DataChunk{data: [0x03]}` (Ctrl+C)
- **THEN** the Agent writes `\x03` to the PTY master fd; the terminal line discipline sends SIGINT to the foreground process group

#### Scenario: ANSI escape sequence injection
- **WHEN** the Agent receives `DataChunk{data: [0x1B, 0x5B, 0x41]}` (Up Arrow: `\x1B[A`)
- **THEN** the Agent writes exactly those 3 bytes to the PTY master fd; the shell or application interprets them as cursor-up

### Requirement: Control character mapping table
The mobile client SHALL maintain a key mapping table that translates virtual keyboard button presses and physical keyboard events to the correct byte sequences. The mapping SHALL include all keys defined in the design document's control character table.

#### Scenario: Physical Bluetooth keyboard connected
- **WHEN** the user presses Ctrl+C on a physical Bluetooth keyboard connected to the mobile device
- **THEN** the browser `keydown` event is captured, the `ctrlKey` modifier is detected, and `\x03` is sent as a `DataChunk`

#### Scenario: Virtual keyboard Esc button
- **WHEN** the user taps the "Esc" button on the virtual keyboard bar
- **THEN** the client sends `DataChunk{data: [0x1B]}`

#### Scenario: Virtual keyboard Up Arrow button
- **WHEN** the user taps the "Up Arrow" button on the virtual keyboard bar
- **THEN** the client sends `DataChunk{data: [0x1B, 0x5B, 0x41]}` (`\x1B[A`)

### Requirement: Session initialization with default size
When the Agent creates a new PTY session, it SHALL set the initial terminal size to the dimensions specified in the `SessionInit` message. If no dimensions are specified, the Agent SHALL default to 80 columns × 24 rows.

#### Scenario: SessionInit with explicit size
- **WHEN** the Agent receives `SessionInit{cols: 100, rows: 30, command: "/bin/bash"}`
- **THEN** the Agent calls `pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 100, X: 800, Y: 480})`

#### Scenario: SessionInit without size
- **WHEN** the Agent receives `SessionInit{cols: 0, rows: 0, command: ""}`
- **THEN** the Agent defaults to 80×24 and calls `pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80, X: 640, Y: 384})`

### Requirement: Bracketed paste mode support
The mobile client SHALL support bracketed paste mode: when pasting clipboard content, the client SHALL wrap the pasted text with `\x1B[200~` (paste start) and `\x1B[201~` (paste end) escape sequences. This allows applications (shells, vim) to distinguish pasted text from typed text.

#### Scenario: Paste into shell with bracketed paste support
- **WHEN** the user pastes "echo hello\n" and the shell supports bracketed paste
- **THEN** the client sends `\x1B[200~echo hello\n\x1B[201~`; the shell treats this as a literal paste (not interpreting special characters within the pasted content)
