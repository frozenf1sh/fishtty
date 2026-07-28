## Why

PTY 伪终端创建后未设置为 raw 模式，内核终端驱动（line discipline）默认启用 `ECHO`，导致每个输入字符被回显两次：一次来自内核驱动（携带 `echoSeq`，被前端剥离），一次来自 shell readline（`echoSeq=0`，穿透本地回显过滤直接写入 xterm.js）。用户在前端终端看到每个字符重复显示，退格键只能删除其中一个副本。

## What Changes

- PTY 创建后立即将终端属性设置为 raw 模式，禁用内核级 `ECHO`、`ICANON` 等不适合终端模拟器的 line discipline 标志
- 引入 `golang.org/x/term` 依赖，使用 `term.MakeRaw()` 操作 PTY master 文件描述符
- 更新 `terminal-rendering` spec，新增 PTY raw 模式配置的需求

## Capabilities

### New Capabilities
<!-- None — this is a bug fix, not a new capability -->

### Modified Capabilities
- `terminal-rendering`: 新增 PTY raw 模式配置需求 —— Agent 端创建 PTY 后必须立即将终端属性设为 raw 模式，禁用内核级回显（`ECHO`）和规范模式（`ICANON`），确保只有 shell readline 产生远程回显

## Impact

- `internal/agent/adapter/pty/terminal.go`：`New()` 函数在 `goPty.StartWithSize()` 后调用 `term.MakeRaw()`
- `go.mod` / `go.sum`：新增 `golang.org/x/term` 依赖
- 行为变化：PTY 子进程环境不再有内核行缓冲和回显，字符逐字节传输，shell readline 独占回显控制权
- 兼容性：无 **BREAKING** 变更，前端和协议层无需任何修改
