## 1. 依赖与构建

- [x] 1.1 添加 `golang.org/x/term` 依赖到 `go.mod`（`go get golang.org/x/term`）
- [x] 1.2 运行 `go mod tidy` 更新 `go.sum`

## 2. PTY raw 模式实现

- [x] 2.1 在 `internal/agent/adapter/pty/terminal.go` 的 `New()` 函数中，`goPty.StartWithSize()` 返回后立即调用 `term.MakeRaw(int(f.Fd()))` 设置 raw 模式
- [x] 2.2 添加对应的 `import` 声明（`golang.org/x/term`）
- [x] 2.3 处理 `term.MakeRaw` 返回的 error（记录 warning 日志但不阻断 PTY 创建，确保回退兼容）

## 3. 验证

- [x] 3.1 本地编译确认无编译错误（`go build ./...`）
- [x] 3.2 运行现有集成测试确认无回归（`go test ./...`）
- [ ] 3.3 端到端验证：前端终端输入单字符确认不再出现双字符显示
- [ ] 3.4 端到端验证：vim/less 等全屏 TUI 程序正常工作
