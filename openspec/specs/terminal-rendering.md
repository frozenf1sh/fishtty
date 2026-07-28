# fishtty Terminal Rendering

## Purpose

确保 xterm.js 终端在移动端和桌面端均能提供工业级渲染性能，正确处理 CJK 字符、全屏 TUI 程序（vim/Claude Code）和低延迟输入。

## Requirements

### Requirement: WebGL 渲染加速
xterm.js SHALL 优先使用 WebGL 渲染器进行终端内容绘制。当 WebGL 不可用时 SHALL 自动回退到 Canvas 渲染器，Canvas 也不可用时回退到默认 DOM 渲染器。

#### Scenario: WebGL 正常加载
- **WHEN** 浏览器支持 WebGL 且 `WebglAddon` 加载成功
- **THEN** 终端使用 GPU 加速渲染
- **AND** 全屏刷新（如 vim `:e` 切换文件）在 16ms 内完成

#### Scenario: WebGL context 丢失
- **WHEN** WebGL context 因 GPU 资源不足而丢失
- **THEN** 终端自动降级到 Canvas 渲染器
- **AND** 用户终端内容不中断

#### Scenario: WebGL 不可用
- **WHEN** 浏览器不支持 WebGL（如旧设备或软件渲染器）
- **THEN** `WebglAddon` 构造抛出异常
- **AND** 终端自动使用 Canvas 渲染器

### Requirement: Unicode 列宽修正
xterm.js SHALL 加载 `Unicode11Addon` 以使用 Unicode 11 标准的字符宽度计算。

#### Scenario: CJK 字符在 vim 中正确显示
- **WHEN** 终端输出包含 CJK 字符（如中文注释、日文文件名）
- **THEN** 每个 CJK 字符占用恰好 2 列宽度
- **AND** 后续字符的列位置不被偏移

#### Scenario: Emoji 字符正确显示
- **WHEN** 终端输出包含全宽 emoji（如 😀、✅）
- **THEN** 每个 emoji 占用正确的列宽
- **AND** 不导致光标位置异常

### Requirement: 交替缓冲区感知的本地回显
本地回显（EchoBuffer）SHALL 在检测到终端进入交替缓冲区（`\x1b[?1049h`）时清空待匹配队列；在退出交替缓冲区（`\x1b[?1049l`）时恢复本地回显的逐字符匹配模式。

#### Scenario: vim 进入交替缓冲区
- **WHEN** 终端接收并解析到 `\x1b[?1049h` 序列
- **THEN** EchoBuffer 清空当前 pending 缓冲区
- **AND** 标记状态为 `inAltBuffer = true`
- **AND** 后续终端输出仍然正常写入 xterm.js

#### Scenario: vim 退出交替缓冲区
- **WHEN** 终端接收并解析到 `\x1b[?1049l` 序列
- **THEN** EchoBuffer 标记状态为 `inAltBuffer = false`
- **AND** 后续用户输入恢复本地回显的逐字符匹配

#### Scenario: 交替缓冲区内的控制字符
- **WHEN** 在交替缓冲区内用户按下 `j`（vim 向下移动）
- **THEN** 本地回显不将 'j' 写入终端（因为 vim 的响应不是简单回显）
- **AND** 按键字节仍然正常发送到服务端

#### Scenario: 正常模式下输入不受影响
- **WHEN** 终端不在交替缓冲区内（`inAltBuffer = false`）
- **THEN** EchoBuffer 行为与当前逻辑一致：本地写入 + 服务端回显前缀匹配

### Requirement: rAF 批量发送输入
客户端的按键输入 SHALL 使用 `requestAnimationFrame` 合并同一渲染帧内的多次输入，合并为单个 DataChunk 发送。

#### Scenario: 同一帧内快速输入多个字符
- **WHEN** 用户在 16ms 内连续输入 "hel" 三个字符
- **THEN** 本地回显立即逐个显示 'h', 'e', 'l'
- **AND** 发送端将 'h', 'e', 'l' 合并为一个包含 "hel" 的 DataChunk
- **AND** 只产生一次 protobuf 序列化和一次 WebSocket 发送

#### Scenario: 单字符输入不增加感知延迟
- **WHEN** 用户在空闲状态下输入单个字符
- **THEN** 该字符在下一帧（≤16ms）时发送
- **AND** 本地回显立即显示该字符
- **AND** 用户体验与立即发送无差异

### Requirement: 虚拟键盘不抢占终端焦点
虚拟键盘的按钮元素 SHALL 在点击时不将浏览器焦点从 xterm.js 终端转移，保持 IME（输入法）composition 不被中断。

#### Scenario: IME 输入中点击虚拟键盘按钮
- **WHEN** 用户正在使用 IME 输入中文/日文/韩文（composition 进行中）
- **AND** 用户点击虚拟键盘上的按钮（如方向键、Esc）
- **THEN** 按键对应的字节序列正常发送到 PTY
- **AND** xterm.js 保持焦点
- **AND** IME composition 不被中断或强制 commit

#### Scenario: 桌面端点击虚拟键盘按钮后键盘输入仍进入终端
- **WHEN** 用户在桌面端浏览器中点击虚拟键盘按钮
- **THEN** 按钮不获取键盘焦点
- **AND** 后续物理键盘输入仍然进入 xterm.js 终端

### Requirement: 全局按键仅作用于活跃 session
多 session 场景下，`document` 级 keydown 监听器 SHALL 仅在所属 TerminalView 可见（`visible=true`）时将数据发送到对应 session。

#### Scenario: 多个 session 时按键仅发送到活跃终端
- **WHEN** 用户拥有 3 个 PTY session（session-A、session-B、session-C）
- **AND** session-B 为当前活跃（visible=true）
- **AND** 用户按下 Enter 键
- **THEN** `\x0d` 仅发送到 session-B
- **AND** session-A 和 session-C 不收到任何数据

#### Scenario: 切换 session 后按键发送到新活跃终端
- **WHEN** 用户从 session-B 切换到 session-C
- **THEN** 后续按键仅发送到 session-C
- **AND** session-A 和 session-B 不收到任何数据
