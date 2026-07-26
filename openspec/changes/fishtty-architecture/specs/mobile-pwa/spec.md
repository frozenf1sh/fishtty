## ADDED Requirements

### Requirement: React + xterm.js PWA shell
The mobile client SHALL be a single-page React application using `@xterm/xterm` for terminal emulation. It SHALL be deployable as a Progressive Web App with a service worker for offline asset caching and an installable web manifest. The application SHALL support iOS Safari, Android Chrome, and desktop Chrome/Firefox browsers.

#### Scenario: First load on mobile browser
- **WHEN** a user navigates to the fishtty-web URL on a mobile device
- **THEN** the PWA loads, the service worker registers, and the app displays a device list (empty on first visit) with an "Add Device" prompt

#### Scenario: Add to Home Screen
- **WHEN** the user chooses "Add to Home Screen" from the browser menu
- **THEN** the PWA installs as a standalone app (no browser chrome), launching in its own window with the fishtty icon

#### Scenario: Offline asset loading
- **WHEN** the user opens the PWA without an internet connection
- **THEN** the app shell (device list UI, cached assets) displays, but connection to the Server shows a "Reconnecting..." indicator

### Requirement: WebGL-accelerated terminal rendering
The client SHALL use `@xterm/addon-webgl` to render terminal output on a GPU-accelerated HTML5 Canvas element. The client SHALL fall back to the DOM-based renderer (`@xterm/addon-dom-renderer` or xterm.js default) if WebGL context creation fails.

#### Scenario: WebGL available on modern device
- **WHEN** the PWA loads on a device with WebGL support (iOS 15+, Android 8+)
- **THEN** `@xterm/addon-webgl` initializes successfully and terminal rendering uses the GPU canvas renderer

#### Scenario: WebGL unavailable or fails
- **WHEN** the PWA loads on a device without WebGL support or WebGL context creation fails
- **THEN** terminal rendering falls back to the DOM renderer without user-visible error; a console warning is logged

### Requirement: Responsive terminal resize
The client SHALL use `@xterm/addon-fit` to recalculate terminal dimensions (columns × rows) whenever the terminal container element's size changes. Resize events SHALL be debounced at 50 ms before sending a `Resize` message to the Server. The client SHALL call `fit()` on: initial terminal mount, window resize, device orientation change, and soft keyboard visibility toggle.

#### Scenario: Device rotates from portrait to landscape
- **WHEN** the user rotates their phone from portrait to landscape while viewing a terminal session
- **THEN** `@xterm/addon-fit` recalculates the terminal dimensions, the client debounces for 50 ms, and sends `Resize{cols, rows}` to the Server

#### Scenario: Soft keyboard opens
- **WHEN** the soft keyboard opens, reducing the visible terminal area
- **THEN** `@xterm/addon-fit` recalculates dimensions and sends an updated `Resize` after the 50 ms debounce

### Requirement: Virtual keyboard bar
The client SHALL render a persistent virtual keyboard bar above the soft keyboard area containing buttons for: Esc, Tab, Up Arrow, Down Arrow, Left Arrow, Right Arrow, Ctrl+C, and Paste. Each button press SHALL send the corresponding control character or ANSI escape sequence to the active PTY session. Long-press on arrow keys SHALL repeat the key at a rate of 10 Hz.

#### Scenario: User taps Ctrl+C button
- **WHEN** the user taps the "Ctrl+C" button on the virtual keyboard bar while session "abc123" is active
- **THEN** the client sends `DataChunk{session_id: "abc123", seq: 0, data: [0x03]}` to the Server

#### Scenario: User long-presses Down Arrow
- **WHEN** the user holds the Down Arrow button for 500 ms
- **THEN** the client sends `\x1B[B` (ANSI cursor-down) repeatedly at 10 Hz until the user releases the button

#### Scenario: User taps Paste button
- **WHEN** the user taps the "Paste" button
- **THEN** the client reads the system clipboard via `navigator.clipboard.readText()`, and sends the clipboard text content as a `DataChunk` to the active session with bracketed paste wrapping (`\x1B[200~` ... `\x1B[201~`)

### Requirement: Device and session management UI
The client SHALL provide a device list view showing all registered devices with their online/offline status. Selecting an online device SHALL display its active PTY sessions. The user SHALL be able to create a new session (opens a new terminal tab) or attach to an existing session. The client SHALL support multiple session tabs per device.

#### Scenario: Viewing device list
- **WHEN** the user opens the app and is connected to the Server
- **THEN** the device list shows all devices registered to the user's account, with a green indicator for online devices and a gray indicator for offline devices

#### Scenario: Creating a new terminal session
- **WHEN** the user selects an online device and taps "New Terminal"
- **THEN** the client sends `SessionInit{cols, rows, command: ""}` (default shell) to the Server, and upon receiving `SessionCreated`, opens a new terminal tab with xterm.js

#### Scenario: Switching between session tabs
- **WHEN** the user has two terminal tabs open (bash on tab 1, claude on tab 2) and taps tab 2
- **THEN** the terminal area displays tab 2's xterm.js instance, the virtual keyboard targets tab 2's session, and tab 1 continues receiving PTY output in the background

### Requirement: WebSocket connection with reconnection
The client SHALL maintain a single WebSocket connection to the Server using binary frames. On disconnect, the client SHALL attempt reconnection with exponential backoff (1s, 2s, 4s, ..., max 10s). On successful reconnect, the client SHALL send `Reattach{session_id, last_ack_seq}` for each active session.

#### Scenario: WebSocket disconnects while session is active
- **WHEN** the WebSocket connection drops while the user has an active terminal session
- **THEN** the client shows a "Reconnecting..." overlay, begins exponential backoff, and on reconnect sends `Reattach` for the active session

#### Scenario: Successful reattach after reconnect
- **WHEN** the client reconnects and sends `Reattach{session_id: "abc123", last_ack_seq: 42}`
- **THEN** the client receives `ReattachData` with catch-up chunks, replays them into xterm.js in order, updates `last_ack_seq`, and begins processing live `DataChunk` messages

### Requirement: Dark theme by default
The client SHALL use a dark color scheme as the default terminal theme (matching common terminal aesthetics: dark background, light text, ANSI color palette). The terminal and UI chrome SHALL support system dark/light mode preference detection.

#### Scenario: First launch appearance
- **WHEN** the user launches the PWA for the first time
- **THEN** the terminal displays with dark background (#1e1e1e), light text (#d4d4d4), and the full 16-color ANSI palette

### Requirement: PWA manifest and service worker
The client SHALL include a `manifest.json` with app name "fishtty", icons at 192px and 512px, `display: standalone`, and `theme_color: #1e1e1e`. The service worker SHALL cache all static assets (JS bundles, CSS, HTML, icons) for offline access and SHALL NOT cache WebSocket connections or terminal data.

#### Scenario: Service worker installation
- **WHEN** the user visits fishtty-web for the first time
- **THEN** the service worker installs, caches the app shell assets, and the browser offers "Add to Home Screen" on subsequent visits

#### Scenario: WebSocket data bypasses service worker
- **WHEN** terminal data arrives over the WebSocket connection
- **THEN** the data is processed directly by the page's JavaScript; the service worker does not intercept or cache the binary frames
