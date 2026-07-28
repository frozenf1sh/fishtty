## Why

两个前端键盘交互缺陷严重影响终端输入体验：(1) 虚拟键盘按钮被点击时浏览器焦点转移导致 IME（输入法）composition 被强制中断，移动端中文/日文/韩文用户无法正常输入；(2) 多 session 时 `document` 级全局 keydown 监听器向所有 session 广播按键，而非仅发送到当前活跃终端。两个问题都源于 VirtualKeyboard 和 Terminal 组件对浏览器焦点和事件作用域处理不当。

## What Changes

- VirtualKeyboard 按钮阻止点击时的焦点转移，保持 xterm.js 持续持有焦点，使 IME composition 不被打断
- Terminal 的全局 keydown 监听器增加活跃 session 判断，只在当前可见 session 时发送数据
- 组件卸载时清理 `resizeTimer`，消除 timer 泄漏导致的潜在错误

## Capabilities

### New Capabilities
<!-- None — these are bug fixes to existing behavior -->

### Modified Capabilities
- `terminal-rendering`: 新增虚拟键盘输入法兼容性需求和按键事件隔离需求。虚拟键盘按钮 SHALL 不抢占终端焦点，全局按键监听器 SHALL 仅对活跃 session 生效。

## Impact

- `web/src/terminal/VirtualKeyboard.tsx`：按钮元素阻止焦点获取
- `web/src/terminal/Terminal.tsx`：keydown handler 增加可见性判断、unmount 时清理 resize timer
- 无协议变更，无 API 变更，向后完全兼容
