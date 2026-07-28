## 1. VirtualKeyboard — IME 焦点保持

- [x] 1.1 给所有 `vk-btn` 按钮添加 `tabIndex={-1}`，从键盘 tab 序中排除
- [x] 1.2 在 `onPointerDown` handler 中调用 `e.preventDefault()` 阻止浏览器默认焦点转移
- [x] 1.3 同步处理 `onMouseDown`（桌面端），同样调用 `e.preventDefault()`

## 2. Terminal — 活跃 session 按键隔离

- [x] 2.1 新增 `visibleRef`（`useRef<boolean>`），在 render 时同步 `visibleRef.current = visible`
- [x] 2.2 在 `handleKey` 函数开头增加 `if (!visibleRef.current) return;` 守卫
- [x] 2.3 在 `useEffect` cleanup 中增加 `clearTimeout(resizeTimerRef.current)` 清理 resize timer

## 3. 构建验证

- [x] 3.1 `npm run build`（或等效前端构建命令）确认无编译错误
