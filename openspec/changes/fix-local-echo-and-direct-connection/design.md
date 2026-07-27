## Context

去掉 Cloudflare Tunnel 后 latench 验证：直连 ~5ms RTT vs CF Tunnel ~200ms RTT。低延迟暴露了 EchoBuffer 的竞态缺陷——服务端回显在 rAF flush 发送输入之前就到达了。同时需要正式的直连部署方案和配套延迟优化。

## Goals / Non-Goals

**Goals:**
1. EchoBuffer 在任何 RTT 下（2ms ~ 500ms）均不产生双击键
2. 直连模式下 PWA 可用（包括跨域场景）
3. WebSocket 传输额外 CPU 开销最小化
4. PTY 大输出场景（`cat bigfile`）消息分片减少

**Non-Goals:**
- 不引入 TLS（后续单独处理）
- 不改动 zsh/fish 等 shell 配置
- 不实现 protobuf Web Worker（后续迭代）

## Decisions

### D1: 序号追踪替代字符匹配

**现状问题**：EchoBuffer 对服务端回显做逐字符前缀匹配。低延迟下 rAF 还没 flush，服务端回显已到达，pending 状态不确定。

**选型**：给 DataChunk 增加 `echo_seq` 字段（uint32）。前端每次 `writeLocal` 递增本地序号，发送时携带当前序号。服务端原样回传。drain 时按序号范围吸收已确认的本地写入。

```
协议扩展（仅新增字段，向后兼容旧字段）：
  DataChunk {
    string session_id = 1;
    uint64 seq = 2;         // PTY 输出序号（Agent→Server）
    bytes data = 3;         // 数据
    uint32 echo_seq = 4;    // 新增：本地回显序号（Mobile→Server→Agent→原样回传）
  }
```

**流程**：
```
1. 用户输入 'l'
   → echoSeq++ (当前=5)
   → term.write('l')     // 立即显示
   → 记录 pending[5] = 'l'

2. rAF flush 发送 DataChunk { data: 'l', echo_seq: 5 }

3. 服务端处理 → PTY → shell → 回显
   → DataChunk { data: '\x1b[...]l', echo_seq: 5 }  // 携带原 echo_seq

4. drain(serverData, echoSeq=5):
   → 找到 pending[5] = 'l' 已存在
   → 从 serverData 中剥离匹配前缀
   → 删除 pending[5]
   → 返回剩余数据写入 xterm.js

5. 若服务端回显在 rAF flush 之前到达（低延迟竞态）：
   → echoSeq 可能存在 pending 中（已 writeLocal 但未 flush）
   → 或者 echoSeq 对应的 pending 已被后续输入覆盖
   → 无论如何，按序号匹配，逻辑确定
```

**替代方案**：
- 继续用字符匹配 + 在 rAF 里加锁：时序复杂，不可靠 → 拒绝
- 完全关闭本地回显：延迟体感倒退 → 拒绝
- 用时间戳匹配：时钟不同步 → 拒绝

**理由**：序号追踪对任何 RTT 都正确，不依赖时序假设。`echo_seq` 为 0 时行为等同于未启用（向后兼容）。

### D2: WebSocket 压缩关闭

**选型**：`gorilla/websocket.Upgrader{EnableCompression: false}`。

**理由**：终端数据（ANSI escape codes + 文本）压缩率低（<20%），但 deflate 的 CPU 开销在每帧都存在。对于 5ms RTT 的直连，带宽不是瓶颈，CPU 才是。关闭压缩省 3-10ms/frame。

### D3: PTY read buffer 增大

**选型**：`buf := make([]byte, 32768)`（从 4096 提升）。

**理由**：`cat` 大文件时，4096 字节 buffer 导致每 4KB 产生一个 DataChunk → protobuf 序列化 → Connect-RPC → relay → WebSocket 一次完整的消息链。32KB buffer 将消息数减少 8x，系统调用减少 8x。

### D4: 直连部署

**选型**：停用 cloudflared。Server 直接暴露 `:8001` 端口。添加 CORS 头支持 PWA 从不同 origin 访问（当 PWA 从 CDN 或本地缓存加载时）。

docker-compose 保留单 server 服务，移除 CF 相关配置。

**理由**：已验证直连延迟远优于 CF Tunnel。ECS 有公网 IP，无需穿透 NAT。

## Risks / Trade-offs

| Risk | Mitigation |
|------|------------|
| `echo_seq` 新增字段导致新旧版本协议不兼容 | 旧客户端不填 echo_seq 时为 0，服务端检测 proto3 默认值跳过序号匹配 |
| 关闭压缩后带宽增加 | 终端数据本身就不大，5-10KB/s 峰值，直连带宽充足 |
| PTY buffer 增大可能延迟小数据响应 | 非阻塞 read，有数据就返回，不等待填满 buffer |
| 直连无 TLS，终端数据明文传输 | 运营商/WiFi 热点可嗅探。用户已知并接受。后续加 Caddy TLS |
