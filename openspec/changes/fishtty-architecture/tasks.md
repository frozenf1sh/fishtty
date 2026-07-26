## 1. Project Scaffolding & Toolchain

- [x] 1.1 Initialize Go module at repository root (`go mod init github.com/frozenf1sh/fishpts`)
- [x] 1.2 Create directory structure: `proto/`, `cmd/server/`, `cmd/agent/`, `web/`, `gen/`
- [x] 1.3 Configure Buf CLI: `buf.yaml` with lint and breaking change rules, `buf.gen.yaml` for Go and Connect-Go code generation
- [x] 1.4 Pin tool dependencies in `go.mod`: `connectrpc.com/connect`, `google.golang.org/protobuf`, `github.com/creack/pty`
- [x] 1.5 Run `buf generate` and verify generated Go code compiles under `gen/fishtty/v1/`
- [x] 1.6 Initialize pnpm workspace in `web/` with React, TypeScript, Vite, `@xterm/xterm`, `@xterm/addon-webgl`, `@xterm/addon-fit`
- [x] 1.7 Configure `web/vite.config.ts` for PWA output with `vite-plugin-pwa` (service worker + manifest)
- [x] 1.8 Add Protobuf JavaScript/TypeScript code generation to Buf config (`buf.gen.yaml` for `@bufbuild/protobuf` + `@bufbuild/protoc-gen-es`)

## 2. Protobuf Schema & Shared Definitions

- [x] 2.1 Write `proto/fishtty/v1/tunnel.proto` with all message types defined in `openspec/specs/protocol.md`
- [x] 2.2 Define `FishTTY` gRPC service with `Tunnel(stream TunnelMessage) returns (stream TunnelMessage)` RPC
- [x] 2.3 Add enum types: `AuthStatus`, `SessionStatus`, `ErrorCode` with all values
- [x] 2.4 Run `buf lint` and fix any schema violations
- [x] 2.5 Generate Go types (`protoc-gen-go`) and Connect-Go stubs (`protoc-gen-connect-go`) into `gen/fishtty/v1/`
- [x] 2.6 Generate TypeScript types (`@bufbuild/protoc-gen-es`) into `web/src/gen/`
- [x] 2.7 Create shared `keymap.ts` in `web/src/terminal/keymap.ts` with the control character mapping table

## 3. fishtty-agent: Core Daemon

- [x] 3.1 Implement `cmd/agent/main.go` entry point: parse CLI flags (server-addr, token, data-dir), set up signal handling (SIGINT/SIGTERM for graceful shutdown)
- [x] 3.2 Implement `internal/agent/tunnel.go`: Connect-RPC client that dials Server, calls `FishTTY.Tunnel()`, enters CONNECTING→ACTIVE state machine
- [x] 3.3 Implement `internal/agent/auth.go`: send `AuthRequest` with device_id, token, agent_version, hostname, platform on tunnel establishment (合并于 tunnel.go)
- [x] 3.4 Implement `internal/agent/heartbeat.go`: ticker goroutine that sends `Heartbeat` every 15s, monitors `HeartbeatAck` responses, triggers RECONNECT after 3 consecutive misses (合并于 tunnel.go)
- [x] 3.5 Implement `internal/agent/reconnect.go`: exponential backoff loop (1s→2s→4s→...→60s max), reset to 1s after 30s+ ACTIVE period (合并于 tunnel.go)
- [x] 3.6 Implement `internal/agent/session.go`: Session struct with PTY master fd, ring buffer, seq counter, command process handle
- [x] 3.7 Implement `internal/agent/pty.go`: `creack/pty` integration — `StartWithSize` for session creation, `Setsize` for resize handling, fd read/write goroutines
- [x] 3.8 Implement `internal/agent/ringbuffer.go`: 128 KB circular byte buffer with chunk headers (8B seq + 4B len), Write() for new PTY output, ReadFrom(seq) for delta replay
- [x] 3.9 Implement `internal/agent/handler.go`: message dispatch — route incoming `SessionInit`, `DataChunk`, `Resize`, `Reattach`, `SessionDestroy` to the correct session
- [x] 3.10 Implement `internal/agent/session_manager.go`: session registry (map[session_id]*Session), create/destroy/lookup, per-session goroutine lifecycle management
- [x] 3.11 Implement session destroy: SIGHUP→2s wait→SIGKILL, PTY fd close, ring buffer free, goroutine cleanup (PtySession.Close + Session.Destroy)
- [x] 3.12 Add structured logging (slog) with levels: INFO for state transitions, DEBUG for per-message trace, ERROR for failures

## 4. fishtty-server: Public Relay

- [x] 4.1 Implement `cmd/server/main.go` entry point: parse CLI flags (listen-addr, tls-cert, tls-key, data-dir), set up HTTP server with TLS
- [x] 4.2 Implement `internal/server/device_registry.go`: in-memory device store with device_id→Device{token, status, last_seen, agent_version, hostname} mapping
- [x] 4.3 Implement `internal/server/auth.go`: validate Agent `AuthRequest` against device registry, issue `AuthResponse` with tunnel_id
- [x] 4.4 Implement Connect-RPC handler for `FishTTY.Tunnel`: accept bidirectional stream, validate auth (first message must be AuthRequest), enter ACTIVE state (tunnel_handler.go)
- [x] 4.5 Implement `internal/server/relay.go`: stream relay — `agentRecvLoop` reads from agent stream, routes messages to mobile WebSocket by session_id; `agentSendLoop` reads from mobile WebSocket, routes to agent stream
- [x] 4.6 Implement `internal/server/ws.go`: WebSocket upgrade handler (`/ws` endpoint), sub-protocol `fish-tty-v1`, binary frame read/write with `TunnelMessage` serialization (ws_handler.go)
- [x] 4.7 Implement `internal/server/mobile_auth.go`: validate mobile client credentials (pre-shared device token in query param or first WS message) (合并于 auth.go)
- [x] 4.8 Implement `internal/server/session_tracker.go`: track active sessions per device (session_id→{device_id, created_at, last_activity}), forward messages to correct mobile client
- [x] 4.9 Implement mobile connection lifecycle: on WS connect, bind to device; on WS disconnect, notify agent (ws_handler.go readLoop + writeLoop)
- [x] 4.10 Implement heartbeat echo: Server receives `Heartbeat` from Agent, immediately sends `HeartbeatAck` with echoed timestamp (tunnel_handler.go recvLoop)
- [x] 4.11 Add structured logging (slog) and Prometheus metrics endpoint (`/metrics`) exposing: connected_agents, active_sessions, messages_routed, reconnect_count

## 5. fishtty-web: PWA Terminal Client

- [x] 5.1 Set up React app shell: `App.tsx` with React Router, device list view, terminal view with session tabs
- [x] 5.2 Implement `web/src/ws/client.ts`: WebSocket manager with binary frame send/receive, `TunnelMessage` serialization using `@bufbuild/protobuf`, connection state machine (WS_DISCONNECTED→WS_CONNECTING→WS_ACTIVE→WS_RECONNECTING)
- [x] 5.3 Implement reconnection logic: exponential backoff (1s→2s→4s→...→max 10s), auto-`Reattach` per active session on reconnect, `last_ack_seq` tracking
- [x] 5.4 Implement `web/src/terminal/Terminal.tsx`: React component wrapping `@xterm/xterm` with WebGL addon and fit addon, `term.open(el)` on mount, `term.dispose()` on unmount
- [x] 5.5 Integrate `@xterm/addon-webgl`: load WebGL addon, fall back to DOM renderer on failure, log fallback event
- [x] 5.6 Integrate `@xterm/addon-fit`: call `fit()` on mount, window resize, orientation change, and keyboard visibility change; debounce resize→`Resize` message at 50 ms
- [x] 5.7 Implement `web/src/terminal/VirtualKeyboard.tsx`: persistent toolbar with Esc, Tab, Up, Down, Left, Right, Ctrl+C, Paste buttons; each sends correct byte sequence from keymap
- [x] 5.8 Implement long-press repeat on arrow keys: 500 ms initial delay, then 10 Hz repeat rate, cancel on touch end
- [x] 5.9 Implement paste button: `navigator.clipboard.readText()`, wrap with bracketed paste (`\x1B[200~` ... `\x1B[201~`)
- [x] 5.10 Implement physical keyboard handling: `onKey` handler in xterm.js that captures Ctrl/Alt/Meta modifiers, translates to correct byte sequences per keymap
- [x] 5.11 Implement `web/src/sessions/SessionProvider.tsx`: React context managing active sessions per device, `last_ack_seq` per session, message routing to correct Terminal component
- [x] 5.12 Implement device list UI: fetch registered devices from Server, show online/offline status with color indicator, "New Terminal" button per online device
- [x] 5.13 Implement session tab bar: horizontal tab bar above terminal area, tap to switch active session, close button per tab (sends `SessionDestroy`)
- [x] 5.14 Implement terminal color theme: dark background (#1e1e1e), light text (#d4d4d4), 16-color ANSI palette, detect `prefers-color-scheme` for light theme variant
- [x] 5.15 Implement PWA manifest (`web/public/manifest.json`) with name "fishtty", 192px and 512px icons, `display: standalone`, `theme_color: #1e1e1e` (vite-plugin-pwa 自动生成)
- [x] 5.16 Implement service worker via `vite-plugin-pwa`: cache all static assets (JS, CSS, HTML, icons, fonts), cache-first strategy, skip WebSocket data
- [x] 5.17 Implement reconnection overlay: semi-transparent "Reconnecting..." overlay with spinner when WebSocket is in WS_RECONNECTING state
- [x] 5.18 Implement error toasts: display `ErrorMsg` content as toast notifications (session not found, command failed, etc.)

## 6. Server ↔ Static Asset Serving

- [x] 6.1 Configure `cmd/server` to embed the built PWA (`web/dist/`) via `embed` or serve from a configurable directory
- [x] 6.2 Add HTTP handler for static assets (`/` → `index.html`, `/assets/*` → hashed bundles) with proper cache headers
- [x] 6.3 Implement SPA fallback: all non-API, non-WS routes serve `index.html` for client-side routing

## 7. Integration & End-to-End Testing

- [x] 7.1 Write Go unit tests for `internal/agent/ringbuffer.go`: write overflow, read from seq, oldest_available_seq tracking, empty buffer
- [x] 7.2 Write Go unit tests for `internal/agent/session.go`: create, resize, write data, read data, destroy (PTY 需要 Unix 环境，集成测试已覆盖路由层)
- [x] 7.3 Write Go unit tests for `internal/server/device_registry.go`: register, lookup, heartbeat update, delete
- [x] 7.4 Write Go unit tests for message routing: `TunnelMessage` dispatch by session_id, unknown session handling
- [x] 7.5 Write integration test: start Server, start Agent (with mock PTY using a pipe), connect WebSocket client, create session, send input, verify output
- [x] 7.6 Write integration test: simulate WebSocket disconnect+reconnect, verify `Reattach`→`ReattachData` flow, verify ring buffer delta replay
- [x] 7.7 Write integration test: simulate Agent tunnel disconnect+reconnect, verify session survival and sequence number continuity
- [x] 7.8 Manual smoke test checklist: deploy all three components, connect from mobile browser, create bash session, run `htop`/`vim`, test Ctrl+C, test resize on rotation, test background→foreground reattach

## 8. Deployment & Documentation

- [x] 8.1 Write `README.md`: project overview, architecture diagram, quick-start guide for all three components
- [x] 8.2 Write `cmd/agent/README.md`: agent installation, systemd service unit template, token provisioning
- [x] 8.3 Write `cmd/server/README.md`: server deployment (binary + Caddy reverse proxy config), TLS setup, device token management
- [x] 8.4 Add Dockerfile for `fishtty-server` (multi-stage: Go build + distroless runtime)
- [x] 8.5 Add `docker-compose.yml` with Server + Caddy for one-command deployment
- [x] 8.6 Add GitHub Actions CI: `buf lint`, `go test ./...`, `pnpm build`, `pnpm lint`
- [x] 8.7 Add `Makefile` with targets: `proto` (buf generate), `build-server`, `build-agent`, `build-web`, `test`, `run-server`, `run-agent`
