## Context

当前 Agent 端通过 `creack/pty.StartWithSize()` 创建 PTY 后，未修改终端属性（termios）。PTY slave 继承内核默认设置：`ECHO` 启用（内核回显每个输入字节）、`ICANON` 启用（规范模式行缓冲）。这导致每个前端输入字符在服务端被回显两次——内核驱动回显和 shell readline 回显——前端的 `EchoBuffer` 只能剥离第一次（带 `echoSeq`），第二次穿透写入 xterm.js，造成用户看到双字符。

`creack/pty` 库仅负责创建 PTY 和设置窗口大小，termios 配置由调用方负责。标准做法是终端模拟器在 PTY 创建后立即设置 raw 模式。

## Goals / Non-Goals

**Goals:**
- PTY 创建后立即将终端属性设为 raw 模式，禁用 `ECHO` 和 `ICANON`
- 消除内核级字符回显，让 shell readline 成为唯一的远程回显源
- 保持与现有前端 `EchoBuffer` 机制的完全兼容

**Non-Goals:**
- 不修改前端 echo 剥离逻辑（`EchoBuffer.drain`）
- 不修改 WebSocket 协议或 protobuf 消息格式
- 不修改 shell 启动参数或环境变量

## Decisions

### Decision 1: 使用 `golang.org/x/term.MakeRaw` 设置 raw 模式

**选择**：在 `pty.New()` 中，`goPty.StartWithSize()` 返回后立即调用 `term.MakeRaw(int(f.Fd()))`。

**备选方案**：
- *使用 `golang.org/x/sys/unix` 直接操作 termios*：更底层但代码量更大，需要手动设置 `Iflag`/`Oflag`/`Cflag`/`Lflag`。`term.MakeRaw` 封装了标准 raw 模式配置，代码更简洁。
- *使用 `creack/pty` 库自带的 raw 模式*：该库未暴露此功能，需要额外依赖。
- *依赖 shell 自己设置 raw 模式*：不可靠。bash/zsh 在检测到 PTY slave 为终端后会设置 raw 模式，但存在时序窗口（shell 初始化期间 PTY 处于 cooked 模式），且非交互式 shell 不会设置 raw 模式。

**原理**：选择 `golang.org/x/term` 因为它是 Go 官方扩展库，`MakeRaw` 语义清晰，一行调用即可完成 raw 模式配置。

### Decision 2: 在 PTY master fd 上操作 termios

**选择**：对 PTY master 的文件描述符调用 `term.MakeRaw`。

**备选方案**：
- *在 slave fd 上操作*：slave fd 在子进程中，父进程操作不便。且 `creack/pty.StartWithSize` 返回的是 master fd。
- *在子进程启动前 vs 启动后操作*：`StartWithSize` 返回后子进程已启动，此时操作 master fd 的 termios 会影响 slave 端的属性（Unix98 PTY 中 master 和 slave 共享 termios）。在启动后立即操作可以确保子进程从一开始就处于 raw 模式。

**原理**：Unix98 PTY 中，对 master 或 slave 任一端设置 termios 都会影响另一端的终端属性。操作 master fd（`f.Fd()`）是 Go 中最直接的方式。

### Decision 3: 仅设置 raw 模式，不保存/恢复原始终端属性

**选择**：不保存原始终端属性用于恢复。

**原理**：PTY 的整个生命周期都需要 raw 模式。PTY 关闭时内核会自动清理 termios 状态，无需手动恢复。

## Risks / Trade-offs

- **[Risk] 非交互式程序行为变化**：某些程序（如 `cat`）在 raw 模式下行为不同（不再行缓冲）。→ **Mitigation**：`cat` 等程序在管道中本就是非缓冲模式，PTY raw 模式下的行为与管道一致。交互式 shell 会在启动后自行微调 termios（如设置 `ISIG` 以支持 Ctrl+C）。

- **[Risk] 信号生成被禁用**：`MakeRaw` 会清除 `ISIG`，禁用 Ctrl+C/ Ctrl+Z 等信号生成。→ **Mitigation**：bash/zsh 在检测到交互式终端后会重新启用 `ISIG`。这些 shell 总是在 raw 模式 PTY 中运行，这是标准行为。

- **[Risk] 输出处理变化**：`MakeRaw` 会禁用 `OPOST`（输出处理），`\n` 不再自动转换为 `\r\n`。→ **Mitigation**：这正是期望行为。PTY 应用（shell、vim 等）本身会输出正确的 `\r\n` 序列，额外的内核转换会导致双重回车。

## Open Questions

- 无。该修复方案与 xterm、iTerm2、Windows Terminal 等所有主流终端模拟器的 PTY 配置方式一致。
