# fishtty System Architecture

## Overview

fishtty is a remote terminal system enabling control of a home PC (behind NAT) from a mobile device through a public relay server. Three components collaborate:

```
┌──────────────────────┐       ┌──────────────────────────────┐       ┌──────────────────────┐
│   🖥 fishtty-agent    │       │     🌐 fishtty-server         │       │   📱 fishtty-web      │
│   (家用 PC, NAT 后)   │       │     (公网 VPS / Cloud)        │       │   (PWA 客户端)         │
│                      │       │                              │       │                      │
│  ┌────────────────┐  │       │  ┌─────────────────────────┐ │       │  ┌──────────────────┐ │
│  │ Session Manager │  │       │  │   Device Registry       │ │       │  │ React SPA        │ │
│  │                │  │       │  │   - Device auth          │ │       │  │ - Device List    │ │
│  │ ┌────────────┐ │  │       │  │   - Heartbeat tracking   │ │       │  │ - Session Tabs   │ │
│  │ │ bash (pts) │ │  │       │  └─────────────────────────┘ │       │  │ - Virtual KB Bar │ │
│  │ │ ring buf   │ │  │       │                              │       │  └────────┬─────────┘ │
│  │ └────────────┘ │  │       │  ┌─────────────────────────┐ │       │           │           │
│  │ ┌────────────┐ │  │       │  │   Stream Relay          │ │       │  WebSocket│           │
│  │ │claude(pts) │ │  │       │  │   - Route by session_id  │ │       │  Binary   │           │
│  │ │ ring buf   │ │  │       │  │   - Protocol bridge      │ │       │  Protobuf │           │
│  │ └────────────┘ │  │       │  │   (Connect-RPC ↔ WS)     │ │◄──────┼───────────┘           │
│  │                │  │       │  └────────────┬────────────┘ │       │                      │
│  └───────┬────────┘  │       │               │              │       │                      │
│          │           │       │               │              │       │                      │
│    creack/pty        │       │  Connect-RPC  │              │       │                      │
│    (PTY master fd)   │       │  Binary Proto │              │       │                      │
│          │           │       │               │              │       │                      │
└──────────┼───────────┘       └───────────────┼──────────────┘       └──────────────────────┘
           │                                   │
           │  Agent initiates TLS connection   │
           │  (the only side that can dial)    │
           └───────────────────────────────────┘
```

## Component Responsibilities

### fishtty-server

- **Deployment**: Public VPS with TLS termination (Caddy/Nginx).
- **Device Registry**: Stores device identities, auth tokens, online/offline status. In-memory for v1; SQLite for persistence.
- **Stream Relay**: Accepts one Connect-RPC bidirectional stream from each Agent. Accepts one WebSocket connection from each mobile client. Routes `TunnelMessage` objects between them by `session_id`.
- **Auth**: Validates device tokens. Issues session tokens for mobile clients (v1: pre-shared keys).
- **Observability**: Exposes Prometheus metrics and structured logging.

### fishtty-agent

- **Deployment**: User's home PC or server (Linux/macOS). Single Go binary, systemd/launchd service.
- **Tunnel Client**: Connect-RPC client. Dials the Server on startup with exponential backoff. Maintains exactly one long-lived bidirectional stream.
- **Session Manager**: Creates and manages multiple PTY sessions. Each session runs a shell or command via `creack/pty`, with a 128 KB ring buffer for output history.
- **Heartbeat**: Sends `Heartbeat` every 15s. Declares connection dead after 3 missed ACKs.

### fishtty-web

- **Deployment**: Static assets served by the Server or a CDN. Single-page React application.
- **Terminal UI**: `@xterm/xterm` with `@xterm/addon-webgl` (GPU rendering) and `@xterm/addon-fit` (responsive resize).
- **WebSocket Client**: Binary WebSocket with Protobuf message serialization. Tracks `last_ack_seq` per session for reattach.
- **Virtual Keyboard Bar**: Persistent toolbar with Esc, Tab, Arrows, Ctrl+C, Paste buttons.
- **PWA**: Service worker for offline shell, web manifest for installability.

## Data Flow

### Session Creation

```
Mobile                          Server                         Agent
  │                               │                              │
  │  SessionInit{cols,rows,cmd}   │                              │
  ├──────────────────────────────►│                              │
  │                               │  SessionInit{cols,rows,cmd}  │
  │                               ├─────────────────────────────►│
  │                               │                              │── pty.StartWithSize(cmd)
  │                               │                              │── allocate ring buffer (128KB)
  │                               │  SessionCreated{session_id}  │
  │                               │◄─────────────────────────────┤
  │  SessionCreated{session_id}   │                              │
  │◄──────────────────────────────┤                              │
  │                               │                              │
  │  xterm.js opens terminal      │                              │
```

### Terminal I/O

```
Mobile                          Server                         Agent
  │                               │                              │
  │  DataChunk{session_id, data}  │                              │
  ├──────────────────────────────►│                              │
  │                               │  DataChunk{session_id, data} │
  │                               ├─────────────────────────────►│
  │                               │                              │── ptmx.Write(data)
  │                               │                              │
  │                               │                              │── data := ptmx.Read(buf)
  │                               │  DataChunk{session_id,       │
  │                               │            seq, data}        │
  │                               │◄─────────────────────────────┤
  │  DataChunk{session_id,        │                              │
  │            seq, data}         │                              │
  │◄──────────────────────────────┤                              │
  │                               │                              │
  │  xterm.write(data)            │                              │
  │  last_ack_seq = seq           │                              │
```

### Reattach on Reconnect

```
Mobile                          Server                         Agent
  │                               │                              │
  │  (WebSocket reconnected)      │                              │
  │                               │                              │
  │  Reattach{session_id,         │                              │
  │           last_ack_seq: 42}   │                              │
  ├──────────────────────────────►│                              │
  │                               │  Reattach{session_id,        │
  │                               │           last_ack_seq: 42}  │
  │                               ├─────────────────────────────►│
  │                               │                              │── scan ring buffer
  │                               │                              │   for chunks seq>42
  │                               │  ReattachData{start_seq,     │
  │                               │              chunks=[43..50]}│
  │                               │◄─────────────────────────────┤
  │  ReattachData{start_seq: 43,  │                              │
  │               chunks=[43..50]}│                              │
  │◄──────────────────────────────┤                              │
  │                               │                              │
  │  xterm.write(chunk.data)      │                              │
  │  for each chunk in order      │                              │
  │                               │                              │
  │  (resume live streaming)      │                              │
  │  DataChunk{seq: 51, data}     │                              │
  │◄──────────────────────────────┤                              │
```

## Protocol Stack

```
┌──────────────────────┐
│   TunnelMessage       │  Protobuf message (oneof)
│   (Application)       │
├──────────────────────┤
│   Connect Protocol    │  Agent↔Server: Connect-RPC binary
│   or WebSocket Binary │  Mobile↔Server: WebSocket frames
├──────────────────────┤
│   HTTP/1.1 or HTTP/2  │  Connect-RPC works over both
│   (with TLS)          │  WebSocket over TLS
├──────────────────────┤
│   TCP                 │
└──────────────────────┘
```

## Security Model

- **Agent→Server**: Mutual TLS (Server presents certificate; Agent can be configured with certificate pinning). Auth token sent as first message on stream.
- **Mobile→Server**: TLS (standard HTTPS/WebSocket). Auth via pre-shared device token or session token issued by Server.
- **Data at rest**: None. Terminal data is not persisted on Server. Ring buffers on Agent are in-memory only.
- **Trust boundary**: The Server is trusted. It sees all PTY data in plaintext. Future: end-to-end encryption between Mobile and Agent with Server as opaque relay.

## Concurrency Model

### Agent Goroutines per Session

```
Session "abc123"
├── readLoop goroutine: ptmx.Read() → ringBuffer.Write() → stream.Send(DataChunk)
├── writeLoop goroutine: stream.Recv() → filter by session_id → ptmx.Write()
└── (PTY process: fork+exec'd child, monitored via Wait4/syscall)
```

The readLoop and writeLoop are independent. The readLoop blocks on `ptmx.Read()` (yielding the goroutine when idle). The writeLoop blocks on `stream.Recv()`. Both exit when the session is destroyed or the Agent disconnects.

### Server Goroutines per Connection

```
Agent Connection
├── agentRecvLoop: stream.Recv() → route to mobile by session_id → ws.WriteMessage()
├── agentSendLoop: ws.ReadMessage() → route by session_id → stream.Send()
└── Mobile connections: one per connected client
    ├── mobileRecvLoop: ws.ReadMessage() → route to agent → stream.Send()
    └── mobileSendLoop: stream.Recv() → route to mobile → ws.WriteMessage()
```

## References

- `openspec/specs/protocol.md` — Protobuf schema and connection state machine
- `openspec/changes/fishtty-architecture/design.md` — Detailed design decisions
