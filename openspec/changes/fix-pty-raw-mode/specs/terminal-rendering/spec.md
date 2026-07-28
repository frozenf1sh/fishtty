## ADDED Requirements

### Requirement: PTY raw 模式配置
Agent 端在创建 PTY 后 SHALL 立即将终端属性设置为 raw 模式，禁用内核级输入回显（`ECHO`）和规范模式行缓冲（`ICANON`），确保 shell readline 成为唯一的远程回显源。

#### Scenario: PTY 创建后处于 raw 模式
- **WHEN** Agent 调用 `pty.New()` 创建新 PTY 会话
- **THEN** PTY 终端属性中 `ECHO` 标志被清除
- **AND** `ICANON` 标志被清除
- **AND** shell 进程的 stdin/stdout 直接逐字节传输

#### Scenario: 用户输入单个字符不出现双重显示
- **WHEN** 用户在前端 xterm.js 中输入一个普通可打印字符（如 'a'）
- **THEN** PTY 内核驱动不产生回显
- **AND** 远程回显仅来自 shell readline
- **AND** 前端的 `EchoBuffer` 能通过 `echoSeq` 正确剥离该回显
- **AND** 用户在终端中只看到一份字符（本地回显）

#### Scenario: shell 可在 raw 模式 PTY 中正常运行
- **WHEN** shell（bash/zsh）在 raw 模式 PTY 中启动
- **THEN** shell 检测到交互式终端并正常初始化 readline/ZLE
- **AND** Ctrl+C / Ctrl+Z 等信号控制字符正常工作（shell 自行启用 `ISIG`）
- **AND** 命令行编辑、历史、补全等功能不受影响
