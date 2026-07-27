## ADDED Requirements

### Requirement: 直连模式
fishtty-server SHALL 支持不经 Cloudflare Tunnel 的直接 HTTP/WebSocket 访问。Server 监听端口对外暴露，PWA 通过 `http://<ip>:<port>` 直连。

#### Scenario: 直连建立 WebSocket
- **WHEN** PWA 使用 `http://<ip>:8001` 作为 Server 地址
- **THEN** WebSocket 连接成功建立
- **AND** 终端会话正常工作

#### Scenario: 跨域 PWA 访问
- **WHEN** PWA 从不同 origin（如 CDN 缓存或本地文件）发起连接
- **THEN** Server 返回包含 `Access-Control-Allow-Origin: *` 的 HTTP 响应头
- **AND** WebSocket upgrade 不被浏览器跨域策略阻止

### Requirement: WebSocket 压缩关闭
Server 端 WebSocket SHALL 禁用 per-message deflate 压缩以降低 CPU 开销。

#### Scenario: 消息不压缩传输
- **WHEN** WebSocket 发送二进制帧
- **THEN** `gorilla/websocket.Upgrader.EnableCompression` 为 `false`
- **AND** 帧负载为原始 protobuf 二进制，不经过 deflate 处理

### Requirement: PTY 读缓冲区增大
Agent 的 PTY read loop SHALL 使用 32768 字节的读缓冲区，减少大输出场景下的消息分片。

#### Scenario: 大文件输出
- **WHEN** PTY 产生大量输出（如 `cat` 一个 100KB 文件）
- **THEN** 每次 `Read()` 调用最多读取 32768 字节
- **AND** 产生的 DataChunk 消息数相比 4096 字节 buffer 减少约 8 倍

### Requirement: Cloudflare Tunnel 关闭
ECS 部署环境 SHALL 停用 cloudflared 进程，docker-compose 移除 CF 相关服务定义。

#### Scenario: 直连部署
- **WHEN** ECS 上执行 `docker compose up -d`
- **THEN** 只有 fishtty-server 容器运行
- **AND** cloudflared 进程不存在或已停止
- **AND** Server 监听 `0.0.0.0:8001` 对外可访问
