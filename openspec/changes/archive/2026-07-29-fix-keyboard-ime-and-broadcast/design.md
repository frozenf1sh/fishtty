## Context

当前 VirtualKeyboard 使用原生 `<button>` 元素，点击时浏览器自动将焦点转移到按钮上，导致 xterm.js 内部的焦点丢失。在移动端，这意味着正在进行的 IME composition（中文拼音、日文假名等）被强制 commit 或丢弃。Terminal 组件在每个实例的 `useEffect` 中向 `document` 注册全局 `keydown` 监听器，多 session 时每个按键事件被所有 TerminalView 实例捕获并向各自 session 发送数据。

## Goals / Non-Goals

**Goals:**
- 虚拟键盘按钮点击不打断 IME composition
- 按键事件仅发送到当前活跃（visible）的 session
- 组件 unmount 时清理所有 timer，不留下悬挂回调

**Non-Goals:**
- 不修改 xterm.js 内部的 IME 处理逻辑
- 不修改 agent/server 协议层
- 不添加新的依赖

## Decisions

### Decision 1: 虚拟键盘按钮用 `onPointerDown` + `preventDefault` 阻止焦点转移

**选择**：在按钮的 `onPointerDown` handler 中调用 `e.preventDefault()` 阻止默认的焦点行为，同时保留 `tabIndex={-1}` 从 tab 序中排除。

```
之前：
  onPointerDown → 浏览器 focus button → xterm 失焦 → IME 中断 ❌

之后：
  onPointerDown → preventDefault → 焦点留在 xterm → IME 继续 ✓
  onMouseDown 同样处理（桌面端）
```

**备选方案**：
- *用 `<div role="button">` 替换 `<button>`*：可行但失去语义可访问性（ARIA 需要额外配置 `aria-pressed`、键盘事件等），改动更大。
- *用 `onClick` 替换 `onPointerDown`*：不可行，`onClick` 在移动端有 300ms 延迟，且 `onClick` 同样会触发焦点转移。
- *使用 CSS `pointer-events` 控制*：不可行，按钮需要响应点击。

**原理**：`preventDefault` 阻止了浏览器的原生 focus 行为，但 `onPointerDown` handler 仍正常执行，按键数据正常发送。最小的改动，最大的效果。

### Decision 2: Keydown handler 增加 visible 判断

**选择**：在 `handleKey` 中检查 `visible` prop（通过 ref 避免闭包陈旧问题），非活跃 session 的 handler 直接 return。

```javascript
const visibleRef = useRef(visible);
visibleRef.current = visible;  // 保持最新值

const handleKey = (e: KeyboardEvent) => {
    if (!visibleRef.current) return;  // 非活跃 session 忽略
    // ... 原有逻辑
};
```

**备选方案**：
- *改为在 container div 上监听而非 document*：不可行，终端需要捕获全局按键（即使焦点在别处），且 xterm.js 本身依赖全局键盘事件。
- *用单一全局 handler + session dispatch*：架构改动大，引入不必要的复杂度。
- *在父组件 App.tsx 中根据 activeSessionId 过滤*：可行但违反关注点分离，且需要额外的 props drilling。

**原理**：用 `useRef` 保持 `visible` 的最新值，避免 `useEffect` 闭包陈旧问题（无需将 `visible` 加入 deps 导致重复注册/注销监听器）。

### Decision 3: Resize timer 在 unmount 时清理

**选择**：在 `useEffect` cleanup 中增加 `clearTimeout(resizeTimerRef.current)`。

**原理**：这是一个标准的 React 资源清理模式，当前代码遗漏了。

## Risks / Trade-offs

- **[Risk] `preventDefault` 可能影响按钮的其他交互** → **Mitigation**：`onPointerUp` / `onPointerLeave` / `onPointerCancel` 的 `handleUp` 仍然正常触发（这些事件不依赖 default behavior）。长按定时器也正常工作（在 `handleDown` 中启动）。
- **[Risk] 桌面端点击按钮后键盘焦点在 button 上** → **Mitigation**：`tabIndex={-1}` 已经将按钮从键盘 tab 序中排除，`preventDefault` 阻止 pointer 焦点获取。xterm.js 持续持有逻辑焦点，键盘事件路由到 xterm.js。
- **[Risk] `visibleRef` 与 React 渲染不同步** → **Mitigation**：`visibleRef.current = visible` 在每次 render 时同步更新，React 保证 render 和 effect 的顺序，因此 ref 值始终与最新 render 一致。
