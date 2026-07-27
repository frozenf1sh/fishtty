## Context

fishtty 的终端渲染基于 xterm.js 5.5，当前仅加载了 `FitAddon`。`@xterm/addon-webgl` 虽在 `package.json` 中但从未导入，`@xterm/addon-unicode11` 未安装。本地回显（`EchoBuffer`）使用纯字符前缀匹配，不理解终端转义序列。WebSocket 层错误处理几乎为空（`onerror` 是 no-op），连接状态机缺少超时和心跳机制。

用户主要使用场景：Android Chrome 浏览器通过 PWA 连接远程开发环境，运行 vim、Claude Code 等全屏 TUI 程序。

Constraints:
- xterm.js 保持在 5.x 主版本，不进行破坏性升级
- WebSocket 子协议 `fish-tty-v1` 不变
- 服务端与 Agent 之间的 Connect-RPC 流协议保持不变
- 不引入新的后端服务或外部依赖

## Goals / Non-Goals

**Goals:**
1. vim/Claude Code 等交替缓冲区 TUI 程序渲染正确、光标跟随正常
2. 打字到屏幕反馈的体感延迟 ≤30ms（本地回显）+ ≤50ms（服务端回显合并）
3. 所有 WebSocket 连接异常均能转化为用户可见的 Toast 错误
4. Session 丢失时自动恢复或给出明确的操作指引
5. Server `sessionOwners` 不泄漏

**Non-Goals:**
- 不实现 WebTransport 替代 WebSocket（浏览器支持未成熟）
- 不实现 Web Worker 中 protobuf 编解码（可后续迭代）
- 不修改 Agent PTY 创建逻辑
- 不修改 Protobuf wire format（仅扩展 enum 值）

## Decisions

### D1: WebGL 为主渲染器，Canvas 自动回退

**选型**: 尝试加载 `WebglAddon`，失败时回退 `CanvasAddon`，再失败则用默认 DOM 渲染器。

```typescript
// 伪代码
try {
  const webgl = new WebglAddon();
  term.loadAddon(webgl);
  webgl.onContextLoss(() => { /* fallback to canvas */ });
} catch {
  term.loadAddon(new CanvasAddon());
}
```

**替代方案**: 
- 仅用 Canvas addon：比 DOM 快但不如 WebGL，对 CJK 无帮助 → 拒绝
- 直接升级到 xterm.js 6.x headless mode：破坏性变更过大 → 拒绝

**理由**: WebGL 在 Android Chrome 上的支持率 >95%，且 `@xterm/addon-webgl` 已内置 context loss 处理。Canvas addon 作为回退覆盖旧设备。

### D2: Unicode11 addon 始终加载

**选型**: 无条件加载 `Unicode11Addon`。xterm.js 文档明确建议对所有含 CJK 内容的终端加载此 addon。

**理由**: 这是一个纯列宽计算修正，零性能开销。CJK 字符在 vim 中非常常见（文件路径、注释、状态栏），不修正则网格对齐必定崩坏。

### D3: 交替缓冲区检测用写入解析钩子

**选型**: 在 `term.parser.registerCsiHandler` 或通过 `onWriteParsed` 回调中检测交替缓冲区切换序列。

```typescript
// 伪代码
let inAltBuffer = false;
term.parser.registerCsiHandler({prefix: '?', final: 'h'}, (params) => {
  if (params.toArray()[0] === 1049) {
    inAltBuffer = true;
    echo.clear();  // 进入交替缓冲区时清空回显
  }
  return false; // 不拦截，让 xterm.js 正常处理
});
term.parser.registerCsiHandler({prefix: '?', final: 'l'}, (params) => {
  if (params.toArray()[0] === 1049) {
    inAltBuffer = false;
  }
  return false;
});
```

**替代方案**:
- 轮询 `onWriteParsed`：deepseek 助手的建议，但轮询有延迟 → 拒绝
- 不做检测，仅在 control keys 时 clear：当前做法，对 TUI 不充分 → 拒绝

**理由**: `registerCsiHandler` 是 xterm.js 5.x 的 public API，在 xterm.js 内部解析阶段拦截，零额外延迟。返回 `false` 表示不消费该序列，xterm.js 继续正常处理。

### D4: rAF 合并输入发送

**选型**: 在 `term.onData` 中使用 `requestAnimationFrame` 合并同一帧内的多次输入。

```typescript
// 伪代码
let pendingInput: Uint8Array[] = [];
let rafScheduled = false;

function flushInput() {
  if (pendingInput.length === 0) return;
  const merged = concatArrays(pendingInput);
  client.sendData(sessionId, merged);
  pendingInput = [];
  rafScheduled = false;
}

term.onData((data) => {
  echo.writeLocal(term, data);  // 本地回显：即时
  pendingInput.push(encoder.encode(data));
  if (!rafScheduled) {
    rafScheduled = true;
    requestAnimationFrame(flushInput);
  }
});
```

**理由**: 同一帧内的多个字符（快速打字）在 16ms 窗口内到达，合并后减少 protobuf 序列化和 WebSocket 写次数。本地回显保持不变，体感无差异。单字符打字时 rAF 在下一帧立即触发，延迟增加 <16ms。

### D5: WebSocket 应用层 Ping/Pong

**选型**: 客户端每 30s 发送一个自定义 ping 帧（非 WebSocket 协议层的 ping），服务端立即回复 pong。客户端 10s 未收到 pong 则视为连接断开。

**替代方案**:
- 依赖 gorilla/websocket 内置 ping handler：服务端未配置 ping interval → 拒绝
- 使用 TunnelMessage Heartbeat 在 WebSocket 层：过于重量级 → 拒绝

**理由**: 轻量级纯文本帧（`"ping"` / `"pong"`），不经过 protobuf 序列化。浏览器 `send()` 方法天然支持字符串。移动端网络切换（WiFi↔蜂窝）时 TCP 连接可能假死，应用层心跳能更快检测。

### D6: ErrorCode 枚举扩展

**选型**: 在 `proto/fishtty/v1/tunnel.proto` 中扩展 `ErrorCode` 枚举：

```protobuf
enum ErrorCode {
  ERROR_CODE_UNSPECIFIED = 0;
  ERROR_CODE_UNAUTHORIZED = 1;
  ERROR_CODE_COMMAND_FAILED = 2;
  ERROR_CODE_SESSION_NOT_FOUND = 3;
  ERROR_CODE_SESSION_LOST = 4;        // 新增：session 已过期/销毁
  ERROR_CODE_AGENT_UNREACHABLE = 5;   // 新增：Agent 不在线
  ERROR_CODE_CHANNEL_FULL = 6;        // 新增：中继通道拥塞
  ERROR_CODE_CONNECTION_TIMEOUT = 7;  // 新增：连接超时
}
```

**理由**: 客户端需要根据错误码决定 UI 行为（重试/新建/等待）。新增枚举值向后兼容——旧客户端会将其视为未知值但不影响反序列化。

### D7: sessionOwners 泄漏修复

**选型**: 在 `connectrpc/handler.go` 的 `recvLoop` 中，当收到 `SessionDestroyed` 消息时调用 `relay.CleanSession(sid)`。

```go
// relay.go 新增方法
func (r *Relay) CleanSession(sid string) {
    r.mu.Lock(); defer r.mu.Unlock()
    delete(r.sessionOwners, sid)
    delete(r.pendingInits, sid)
}
```

**理由**: 当前 `sessionOwners` 仅在 `UnregisterAgent` 时清理。Session 在 Agent 端被销毁后（PTY 退出、用户主动销毁），Server 端映射残留导致指标虚高且可能路由错误。

## Risks / Trade-offs

| Risk | Mitigation |
|------|------------|
| WebGL addon 在某些 Android 设备上 context loss 频繁 | Canvas addon 自动回退，context loss 时降级 |
| Unicode11 addon 可能导致某些 Unicode 字符宽度与旧版本不同 | 这是修正而非回归——旧行为本身就是错的。若出现兼容问题，可通过 feature flag 关闭 |
| rAF 合并可能使单字符回显延迟增加 ~16ms | 本地回显已即时显示，这 16ms 仅影响服务端收到数据的时机，用户看不到 |
| 新增 ErrorCode 枚举值可能导致旧客户端警告 | protobuf 未知枚举值反序列化为 0（UNSPECIFIED），不会崩溃 |
| `registerCsiHandler` 是 xterm.js 5.x 内部 API，可能有稳定性风险 | 这是 documented public API，xterm.js 官方示例中使用。若未来版本废弃，回退到 `onWriteParsed` 轮询 |
| 应用层 ping/pong 增加网络流量 | 每 30s 约 10 字节，可忽略 |
