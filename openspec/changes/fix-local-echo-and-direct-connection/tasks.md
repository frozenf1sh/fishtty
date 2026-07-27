## 1. Wire Format — echo_seq 协议扩展

- [x] 1.1 扩展 `proto/fishtty/v1/tunnel.proto`：DataChunk 新增 `uint32 echo_seq = 4`
- [x] 1.2 运行 `buf generate` 重新生成 Go 和 TypeScript 代码
- [x] 1.3 Agent `session.go`：readLoop 回传 DataChunk 时保留 `echo_seq` 原值

## 2. EchoBuffer 重写 — 序号追踪

- [x] 2.1 重写 `Terminal.tsx` EchoBuffer 类：用 `Map<number, string>` 替代字符前缀匹配
- [x] 2.2 `writeLocal`：递增 `echoSeq`，记录 `pending.set(echoSeq, data)`，立即 term.write(data)
- [x] 2.3 `drain(serverData, echoSeq)`：按序号查找 pending，匹配时剥离前缀并删除条目，未匹配时透传全部数据
- [x] 2.4 交替缓冲区模式下：drain 直接透传，writeLocal 不写入终端（保持现有逻辑）
- [x] 2.5 `clear()`：清空 pending map 和 echoSeq
- [x] 2.6 序号溢出保护：`echoSeq` 达到 uint32 最大值时回绕归零，同时清空 pending map

## 3. 发送端 — 携带 echo_seq

- [x] 3.1 `Terminal.tsx` 的 rAF flush：每次 flush 使用一个 batched echoSeq 范围（首字符的序号作为 batch 的 echo_seq）
- [x] 3.2 `client.ts` `sendData()`：接受可选的 `echoSeq` 参数，填充 `DataChunk.echo_seq`
- [x] 3.3 `App.tsx` `handleServerMessage`：drainEcho 传入 `chunk.echo_seq`

## 4. 延迟优化

- [x] 4.1 `websocket/handler.go`：`EnableCompression` 改为 `false`
- [x] 4.2 `agent/service/session.go`：read buffer 4096 → 32768

## 5. 直连部署

- [x] 5.1 ECS 上停用 cloudflared：保留（用户有其他用途），PWA 改直连 IP
- [x] 5.2 更新 ECS 上的 docker-compose.yml：已是精简单 server 服务
- [ ] 5.3 构建 + 推送新镜像到 ghcr.io，重建 server 容器并验证 `/health`
