## ADDED Requirements

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
