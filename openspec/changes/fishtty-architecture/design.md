## Context

fishtty is a greenfield remote terminal system with three components: a public Go relay server (`fishtty-server`), a per-PC Go agent daemon behind NAT (`fishtty-agent`), and a mobile-first web client (`fishtty-web`). The agent initiates a single outbound connection to the server, which multiplexes multiple PTY sessions. The mobile client connects to the server over a standard WebSocket.

Key architectural constraints:
- The Agent is always behind NAT/firewall. It MUST initiate the connection outward. The Server can never dial the Agent.
- A single TCP connection from Agent→Server carries all multiplexed PTY sessions plus control traffic.
- The mobile client experiences frequent, brief disconnections (app backgrounding, cell-tower handoff, WiFi→5G transitions).
- No native mobile app distribution — the client is a PWA served as static assets alongside or behind the Server.

## Goals / Non-Goals

**Goals:**
- Define a binary Protobuf framing protocol over Connect-RPC bidirectional streams for Agent↔Server communication.
- Implement per-PTY-session ring buffers (128 KB) on the Agent to preserve terminal output across client disconnections.
- Enable seamless reattach: mobile client sends `Reattach(session_id, last_ack_seq)`, Agent replays missed output from the ring buffer.
- Deliver a PWA terminal client with GPU-accelerated rendering (WebGL) and responsive terminal resize.
- Throttle resize events at 50 ms, inject control characters as raw bytes into the PTY master fd.
- Define canonical architecture and protocol specs in `openspec/specs/`.

**Non-Goals:**
- Native mobile app (Expo/React Native) — we are deliberately choosing PWA.
- Persistent session storage on disk — ring buffer is in-memory only; Agent restart loses history.
- File transfer, port forwarding, or SSH tunneling — pure terminal access only in v1.
- Multi-user or team collaboration features.
- End-to-end encryption beyond TLS — the Server is trusted to relay plaintext PTY data.
- Horizontal scaling of the Server — single-instance relay for v1.

## Decisions

### Decision 1: Connect-RPC (Connect-Go) for Agent↔Server

**Choice**: Connect-RPC with the Connect protocol (binary Protobuf over HTTP/1.1 or HTTP/2).

**Alternatives considered**:

| Approach | Pros | Cons |
|----------|------|------|
| Pure WebSocket + custom binary framing | Simplest to implement from scratch; `gorilla/websocket` is mature | Must invent multiplexing, flow control, message routing, heartbeat semantics. Spec drift inevitable. |
| gRPC (native) | Full HTTP/2 multiplexing; mature Go tooling | Requires HTTP/2+TLS always; gRPC-specific headers can confuse reverse proxies; `grpc-go` is a heavy dependency. |
| **Connect-RPC (chosen)** | Works over HTTP/1.1 or HTTP/2; same Protobuf schema; `Connect-Go` is lighter than `grpc-go`; passes through Caddy/Nginx/Cloudflare cleanly; supports bidirectional streaming via `BidiStream` | Newer ecosystem than gRPC; fewer blog posts / community examples |

**Rationale**: Connect-RPC generates vanilla HTTP requests with `Content-Type: application/connect+proto`. This means any HTTP reverse proxy (Caddy, Nginx, Cloudflare) can handle them without special gRPC configuration. The Connect protocol supports bidirectional streaming over HTTP/1.1 with chunked transfer encoding or over HTTP/2 with native multiplexing — the same generated code works for both. We also get gRPC and gRPC-Web compatibility as fallback (via `WithGRPC()` / `WithGRPCWeb()` client options).

**Architecture note**: The Agent is the Connect-RPC *client* (it initiates the connection). The Server exposes the Connect-RPC *handler*. This is the reverse of a typical client-server RPC setup, but Connect-Go's `BidiStream` works symmetrically — both sides can send and receive independently after the stream is established.

### Decision 2: Single Stream, Message Multiplexing via `oneof`

**Choice**: One Connect-RPC bidirectional streaming RPC with a Protobuf `oneof` payload covering all message types.

```
service FishTTY {
  rpc Tunnel(stream TunnelMessage) returns (stream TunnelMessage);
}
```

**Alternative considered**: Separate RPCs per session (create a new stream per PTY). Rejected because the Agent establishes a single TCP connection; creating N HTTP streams would require N HTTP/2 streams over the same connection anyway, and the single-stream approach simplifies the Agent's connection lifecycle (one dial, one reconnection loop).

**Message types in the `oneof`**:

| Message | Direction | Purpose |
|---------|-----------|---------|
| `AuthRequest` / `AuthResponse` | Agent→Server / Server→Agent | Device authentication on tunnel establishment |
| `SessionInit` / `SessionCreated` | Server→Agent / Agent→Server | Create a new PTY session |
| `DataChunk` | Bidirectional | Raw PTY stdin/stdout bytes with sequence number |
| `Resize` | Server→Agent | PTY window size change (rows, cols) |
| `Heartbeat` / `HeartbeatAck` | Bidirectional | Liveness check |
| `Reattach` | Server→Agent | Client reconnects; requests delta replay from ring buffer |
| `ReattachData` | Agent→Server | Replayed historical data from ring buffer |
| `SessionDestroy` | Server→Agent | Destroy a PTY session |
| `Error` | Bidirectional | Error reporting |

### Decision 3: Ring Buffer for Output History

**Choice**: 128 KB per-session in-memory ring buffer with sequence-numbered chunks.

**Design**:

```
┌─────────────────── Ring Buffer (128 KB) ───────────────────┐
│                                                            │
│  oldest → [chunk:seq=102] [chunk:seq=103] ... [chunk:seq=N]│
│  head ──────────────────────────────────────────────→      │
│                                                            │
│  Each chunk header (before protobuf serialization):         │
│  ┌──────────┬──────────┬────────────────────────────────┐  │
│  │ seq (u64)│ len(u32) │ raw PTY output bytes            │  │
│  └──────────┴──────────┴────────────────────────────────┘  │
│                                                            │
│  Total overhead per chunk: 12 bytes (seq + len)            │
│  Typical chunk size: 1-4 KB (PTY read granularity)         │
│  So ~28-120 chunks fit in 128 KB                           │
│                                                            │
│  seq starts at 0 per session, monotonically increasing     │
│  (uint64, wraps are acceptable after 2^64 chunks)          │
└────────────────────────────────────────────────────────────┘
```

**Why 128 KB**: Empirical measurement of terminal output patterns — 128 KB captures ~30-120 seconds of typical interactive terminal output (build logs, `tail -f`, vim editing). Larger buffers would help with `cat`-ing large files, but the reattach use case targets interactive sessions, not bulk data transfer. 128 KB keeps memory bounded with many sessions.

**Sequence number semantics**: Each `DataChunk` from Agent→Server carries a monotonically incrementing `seq` number. The mobile client (via the Server) tracks the highest `seq` it has successfully displayed. On reattach, the client sends `Reattach(session_id, last_ack_seq)`. The Agent replays all chunks with `seq > last_ack_seq` that still exist in the ring buffer, then resumes live streaming.

**What happens when the ring buffer has overwritten needed data**: The Agent replays from the oldest available chunk. The mobile client's terminal will show a gap (the overwritten portion is lost), but this is acceptable: the user sees the freshest available history, and the terminal is in a usable state. The `ReattachData` message includes the actual starting `seq` so the client knows if a gap occurred.

### Decision 4: Mobile ↔ Server: WebSocket with Protobuf Binary Frames

**Choice**: WebSocket with binary frames carrying serialized Protobuf messages.

The Server acts as a protocol bridge:
```
Agent ←──Connect-RPC (binary Protobuf)──→ Server ←──WebSocket (binary Protobuf)──→ PWA Client
```

The WebSocket sub-protocol is `fish-tty-v1`. Each binary WebSocket frame is a serialized `TunnelMessage` (the same Protobuf message used on the Agent side). The Server unpacks, routes by `session_id`, and repacks messages between the two transports.

**Why not JSON**: JSON+Base64 adds 33% encoding overhead to terminal data (every 3 bytes become 4 base64 chars + JSON boilerplate). For ANSI-heavy terminal output, this is a meaningful bandwidth and CPU cost on both mobile and server. Binary Protobuf frames carry `bytes` fields with zero encoding overhead.

### Decision 5: PWA with React + xterm.js + WebGL

**Choice**: Single-page React app using `@xterm/xterm` with `@xterm/addon-webgl` and `@xterm/addon-fit`.

**Architecture**:

```
┌─────────────────────────────────────────────────────┐
│                 PWA (React SPA)                      │
│                                                     │
│  ┌───────────────────────────────────────────────┐  │
│  │  App Shell                                     │  │
│  │  ┌─────────┐ ┌──────────────────────────────┐ │  │
│  │  │ Device  │ │  Terminal Area                │ │  │
│  │  │ List    │ │  ┌──────────────────────────┐ │ │  │
│  │  │         │ │  │  xterm.js + WebGL addon  │ │ │  │
│  │  │ PC-1 ●  │ │  │  (GPU-rendered canvas)   │ │ │  │
│  │  │ PC-2 ○  │ │  │                          │ │ │  │
│  │  │         │ │  │  user@home:~$ _          │ │ │  │
│  │  │         │ │  └──────────────────────────┘ │ │  │
│  │  │         │ │                               │ │  │
│  │  │ [+ New] │ │  ┌──────────────────────────┐ │ │  │
│  │  └─────────┘ │  │  Virtual Keyboard Bar     │ │ │  │
│  │               │  │ [Esc][Tab][▲][Ctrl+C][P]│ │ │  │
│  │               │  │ [◀][▼][▶]     [Paste]   │ │ │  │
│  │               │  └──────────────────────────┘ │ │  │
│  │               └──────────────────────────────┘ │  │
│  └───────────────────────────────────────────────┘  │
│                                                     │
│  ┌───────────────────────────────────────────────┐  │
│  │  WebSocket Manager (connection state machine)  │  │
│  │  - Auto-reconnect with exponential backoff     │  │
│  │  - Reattach on reconnect                       │  │
│  │  - Sequence number tracking per session        │  │
│  └───────────────────────────────────────────────┘  │
│                                                     │
│  ┌───────────────────────────────────────────────┐  │
│  │  Service Worker                                │  │
│  │  - Static asset caching                        │  │
│  │  - Install prompt (Add to Home Screen)         │  │
│  │  - (WebSocket is NOT in SW — lives in page)    │  │
│  └───────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────┘
```

**Why WebGL addon**: `@xterm/addon-webgl` renders terminal output on a GPU-accelerated `<canvas>` instead of the DOM-based renderer. For a terminal that rapidly outputs ANSI-colored text (build output, syntax-highlighted `cat`), the GPU renderer significantly reduces frame time and battery drain on mobile. Fallback to DOM renderer is automatic if WebGL is unavailable.

**Why @xterm/addon-fit**: The `fit` addon recalculates terminal dimensions (cols × rows) based on the container element's pixel size and the current font metrics. This is essential for mobile: screen rotation, soft keyboard appearance, and browser resize all change the available terminal area. The `fit` addon triggers a `Resize` message to the Agent.

### Decision 6: Connection State Machine

The Agent's tunnel connection has three states:

```
                    ┌──────────┐
     start ────────▶│DISCONNECTED│
                    └─────┬────┘
                          │ dial() succeeds
                          ▼
                    ┌──────────┐
           ┌───────│CONNECTING│───────┐
           │       └─────┬────┘       │
           │             │ Auth fails │
           │             ▼            │
           │       ┌──────────┐       │
           │       │  ACTIVE   │       │
           │       └─────┬────┘       │
           │             │            │
           │    ┌────────┼────────┐   │
           │    │        │        │   │
           │    ▼        ▼        ▼   │
           │  Stream  Heartbeat  Err  │
           │  Error   Timeout         │
           │    │        │        │   │
           │    └────────┼────────┘   │
           │             ▼            │
           │       ┌──────────┐       │
           └──────▶│RECONNECT  │◄──────┘
                   │(backoff)  │
                   └───────────┘
```

- **DISCONNECTED**: No active connection. Agent periodically attempts to dial.
- **CONNECTING**: TCP/TLS established, `AuthRequest` sent, awaiting `AuthResponse`.
- **ACTIVE**: Authenticated, bidirectional stream open. PTY sessions may be created. Heartbeats every 15 seconds.
- **RECONNECT**: Stream broken. Exponential backoff (1s → 2s → 4s → ... → max 60s). PTY sessions continue running. On reconnect, Server re-sends `SessionInit` for each existing session; Agent responds with `SessionCreated` (and begins live streaming from current PTY state).

The mobile client has a parallel state machine (WebSocket connected/disconnected/reconnecting) plus per-session `last_ack_seq` tracking.

### Decision 7: Resize Throttle

**Choice**: 50 ms debounce on the client side before sending `Resize`.

**Implementation**:

```
xterm.js fit() event
      │
      ▼
  debounce(50ms)
      │
      ▼ (only fires once after 50ms of no resize events)
  send Resize{cols, rows} via WebSocket
      │
      ▼
  Server forwards to Agent
      │
      ▼
  Agent: pty.Setsize(ptmx, &pty.Winsize{Rows, Cols, X: Cols*8, Y: Rows*16})
```

The 50 ms window captures the final dimensions after a rapid resize sequence (e.g., keyboard animation), avoiding a burst of intermediate resize messages. The `X` and `Y` pixel fields are derived from `Cols*8` and `Rows*16` as sensible defaults for font-cell dimensions.

### Decision 8: Control Character Mapping

Terminal control keys are injected by writing the corresponding ASCII/ANSI byte sequence to the PTY master file descriptor. No OS-level signal syscalls are used.

| Key | Bytes Sent | Effect |
|-----|------------|--------|
| Ctrl+C | `\x03` | SIGINT to foreground process group |
| Ctrl+D | `\x04` | EOF on stdin |
| Ctrl+Z | `\x1A` | SIGTSTP (suspend) |
| Ctrl+\ | `\x1C` | SIGQUIT |
| Esc | `\x1B` | Escape (prefix for ANSI sequences) |
| Tab | `\x09` | Horizontal tab |
| Enter | `\x0D` | Carriage return |
| Backspace | `\x7F` | Delete backward |
| Up Arrow | `\x1B[A` | Cursor up |
| Down Arrow | `\x1B[B` | Cursor down |
| Right Arrow | `\x1B[C` | Cursor forward |
| Left Arrow | `\x1B[D` | Cursor backward |
| Home | `\x1B[H` | Beginning of line |
| End | `\x1B[F` | End of line |
| Page Up | `\x1B[5~` | Scroll up |
| Page Down | `\x1B[6~` | Scroll down |

All mappings are defined in a shared `keymap.ts` module on the PWA client. The virtual keyboard bar renders buttons for the most frequently needed keys (Esc, Tab, arrows, Ctrl+C) and allows custom key bindings.

## Risks / Trade-offs

- **[Ring buffer too small for some workloads]**: `cat`-ing a multi-MB log file will overflow the 128 KB ring buffer within seconds. The reattach client will see a gap. Mitigation: document that reattach is designed for interactive sessions, not bulk output. Future: make buffer size configurable per session.

- **[Connect-RPC maturity]**: Connect-Go (v1.x, 2025-2026) has a smaller ecosystem than gRPC. Mitigation: the generated code is compatible with gRPC and gRPC-Web; we can switch transports without changing the Protobuf schema. The `connectrpc.com/connect` package API is stable (v1.x).

- **[Server as single point of failure]**: If the Server restarts, all Agent tunnels drop and all mobile clients disconnect. Mitigation: v1 accepts this. Agents auto-reconnect with exponential backoff. Future: Server clustering with session migration.

- **[PWA background limitations]**: iOS Safari aggressively suspends pages; the PWA will disconnect when backgrounded. This is mitigated by the ring buffer + reattach mechanism — the disconnect is not user-visible; upon returning to the app, the terminal catches up from the buffer.

- **[Protobuf versioning]**: Changing the Protobuf schema requires coordinated Agent + Server + Client updates. Mitigation: Protobuf's backward-compatible evolution rules (additive fields, reserved field numbers). The `TunnelMessage` oneof can be extended with new message types without breaking old clients.

## Open Questions

1. **Auth mechanism**: Token-based (pre-shared key per device) vs. OAuth/OIDC (login with GitHub/Google)? Token-based is simpler for v1. Decision deferred to implementation.

2. **Mobile client ↔ Server auth**: Same token? Separate session token issued by Server after mobile login? Needs design.

3. **Multiple mobile clients per device**: Should two phones be able to attach to the same PTY session simultaneously (shared terminal)? v1: no — one mobile connection per session. Future consideration.

4. **TLS certificate management for Agent**: The Agent needs to trust the Server's TLS certificate. Auto-generated self-signed cert with pinning? Let's Encrypt with public CA? Depends on deployment model.

5. **Monitoring/observability**: What metrics should the Server expose? (Connected agents count, active sessions, message throughput, reconnect rate.) Prometheus endpoint for v1?
