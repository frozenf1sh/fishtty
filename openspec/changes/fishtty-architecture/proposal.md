## Why

fishtty aims to be a reliable, low-latency remote terminal system for controlling a home PC from a mobile device through a public relay server. Existing solutions (SSH + tmux over a reverse tunnel) are cumbersome to set up, lack mobile-optimized interaction, and handle network interruption poorly. We need a purpose-built system with binary-efficient transport, tmux-like session persistence, and a mobile-first terminal UI — but without the operational burden of native app distribution or background keep-alive hacks.

## What Changes

- **Protocol Layer**: Replace the legacy JSON+Base64 encoding with a unified Protobuf binary framing protocol over Connect-RPC (Connect-Go) bidirectional streams, ensuring minimal wire overhead and compatibility with standard HTTP reverse proxies (Caddy, Nginx, Cloudflare).

- **Session Persistence via Ring Buffer**: Each PTY session on the Agent maintains a 128 KB in-memory ring buffer. When the mobile client disconnects and reconnects, the `Reattach` message triggers delta replay from the buffer using sequence-number-based catch-up — achieving tmux-like seamless recovery without native background services.

- **Mobile Client as PWA**: Ship the client as a React + xterm.js Progressive Web App with WebGL-accelerated terminal rendering (`@xterm/addon-webgl`), responsive resize via `@xterm/addon-fit`, and a custom virtual keyboard bar for terminal-specific keys (Esc, Tab, Ctrl+C, arrows, paste). No App Store required.

- **Control Flow Hardening**: Resize events are throttled at 50 ms on the client before propagation to the server and PTY. Control characters follow standard ANSI/terminal mappings for predictable behavior across shells and TUI applications.

- **Specification Artifacts**: Establish `architecture.md` (system topology and data flow) and `protocol.md` (Protobuf schema, connection state machine) as canonical specs in the main `openspec/specs/` directory.

## Capabilities

### New Capabilities

- `agent-tunnel`: Agent-to-Server reverse tunnel with Connect-RPC bidirectional stream, device registration, authentication, heartbeat, and multiplexed PTY data channels.
- `protocol-binary-frames`: Protobuf message schema defining `SessionInit`, `DataChunk`, `Resize`, `Heartbeat`, `Reattach`, and the connection state machine with sequence-number tracking.
- `pty-ring-buffer`: Per-session 128 KB ring buffer on the Agent for output history retention, enabling delta replay on `Reattach` via `last_ack_seq`.
- `mobile-pwa`: React + xterm.js PWA with WebGL rendering, fit addon, virtual keyboard bar, session list, and reattach-aware WebSocket client.
- `control-signals`: Resize throttling (50 ms), ANSI control character mapping, signal injection via PTY master write, and PTY size synchronization.

### Modified Capabilities

<!-- No existing capabilities to modify — this is the initial architecture establishment. -->

## Impact

- **New services**: `fishtty-server` (Go, public relay), `fishtty-agent` (Go, per-PC daemon), `fishtty-web` (React PWA)
- **New dependencies**: `connectrpc.com/connect` (Connect-Go), `google.golang.org/protobuf`, `github.com/creack/pty`, `github.com/gorilla/websocket`, `@xterm/xterm` + `@xterm/addon-webgl` + `@xterm/addon-fit`
- **Infrastructure**: Server requires a public-facing endpoint with TLS termination (Caddy/Nginx). Agent requires outbound internet access only. PWA is served as static assets.
- **Development toolchain**: Buf CLI for Protobuf compilation, pnpm for frontend, Go 1.22+ for backend
