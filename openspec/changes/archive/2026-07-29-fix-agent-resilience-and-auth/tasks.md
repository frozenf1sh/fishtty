## 1. Device Registry token 校验

- [x] 1.1 在 `Register()` 的设备已存在分支中增加 token 比较，不匹配时返回 `fmt.Errorf("设备 %s token 不匹配，请检查配置文件", deviceID)`
- [x] 1.2 确保首次注册路径（设备不存在时）不受影响

## 2. Agent 隧道 panic 恢复触发重连

- [x] 2.1 在 `connect()` 中将 `defer cancel()` 替换为显式 cancel：在每个 goroutine 的 panic recover 中调用 `cancel()`
- [x] 2.2 新增 `tunnelCancel` 变量捕获 `cancel` 函数，在 sendLoop 和 recvLoop 的 recoverLog 中调用
- [x] 2.3 确保 `wg.Wait()` 不受影响——goroutine 退出时仍正确调用 `wg.Done()`

## 3. 验证

- [x] 3.1 `go build ./...` 确认编译通过
- [x] 3.2 `go test ./...` 确认现有测试无回归
