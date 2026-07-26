package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	fishttyv1connect "github.com/frozenf1sh/fishpts/gen/fishtty/v1/fishttyv1connect"
	"golang.org/x/net/http2"
)

// ── 隧道状态 ──

// TunnelState 表示 Agent↔Server 连接的状态。
type TunnelState int

const (
	StateDisconnected TunnelState = iota
	StateConnecting
	StateActive
	StateReconnecting
)

func (s TunnelState) String() string {
	switch s {
	case StateDisconnected:
		return "DISCONNECTED"
	case StateConnecting:
		return "CONNECTING"
	case StateActive:
		return "ACTIVE"
	case StateReconnecting:
		return "RECONNECTING"
	default:
		return "UNKNOWN"
	}
}

// ── 常量 ──

const (
	// 心跳间隔
	heartbeatInterval = 15 * time.Second
	// 心跳超时（连续多少次未收到 ACK 视为断连）
	heartbeatMissThreshold = 3
	// 发送通道缓冲区大小
	sendChSize = 256
	// 重连初始延迟
	reconnectMinDelay = 1 * time.Second
	// 重连最大延迟
	reconnectMaxDelay = 60 * time.Second
	// 稳定连接多少时间后重置退避
	reconnectResetAfter = 30 * time.Second
)

// ── Tunnel ──

// Tunnel 封装了 Agent 到 Server 的 Connect-RPC 双向流隧道。
// 负责：连接管理、认证、心跳、指数退避重连、消息收发。
type Tunnel struct {
	serverAddr string
	deviceID   string
	token      string
	agentVer   string
	hostname   string
	platform   string
	httpClient *http.Client
	client     fishttyv1connect.FishTTYClient

	sendCh   chan *fishttyv1.TunnelMessage // 统一发送通道
	state    TunnelState
	stateMu  sync.RWMutex

	// 心跳追踪
	heartbeatAckCh chan int64 // 收到 HeartbeatAck 时通知（携带时间戳）

	// 重连信号
	reconnectCh chan struct{} // 触发重连

	logger    *slog.Logger
	handler   *Handler
	sessionMgr *SessionManager
}

// TunnelConfig 定义 Tunnel 的创建参数。
type TunnelConfig struct {
	ServerAddr string // Server 地址，如 "https://fishtty.example.com"
	DeviceID   string // 设备唯一标识
	Token      string // 预共享认证令牌
	AgentVer   string // Agent 版本
	Hostname   string // 主机名
}

// NewTunnel 创建一个新的 Tunnel 实例。
// 此时尚未建立连接，需要调用 Run() 启动。
func NewTunnel(cfg TunnelConfig) *Tunnel {
	// 构建支持 h2c 的 HTTP 客户端。
	// 如果 Server 地址是 http://（非 TLS），使用 HTTP/2 Cleartext 传输层。
	httpClient := buildHTTPClient(cfg.ServerAddr)

	t := &Tunnel{
		serverAddr:     cfg.ServerAddr,
		deviceID:       cfg.DeviceID,
		token:          cfg.Token,
		agentVer:       cfg.AgentVer,
		hostname:       cfg.Hostname,
		platform:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		httpClient:     httpClient,
		sendCh:         make(chan *fishttyv1.TunnelMessage, sendChSize),
		heartbeatAckCh: make(chan int64, 16),
		reconnectCh:    make(chan struct{}, 1),
		logger:         slog.Default().With("component", "tunnel"),
	}
	t.client = fishttyv1connect.NewFishTTYClient(t.httpClient, cfg.ServerAddr)
	t.sessionMgr = NewSessionManager(t.sendCh, t.logger)
	t.handler = NewHandler(t.sessionMgr, t.sendCh, t.logger)
	t.setState(StateDisconnected)
	return t
}

// Run 启动 Tunnel 主循环：连接 → 运行 → 重连 → 循环。
// 此方法会阻塞，直到 ctx 被取消。
func (t *Tunnel) Run(ctx context.Context) error {
	t.logger.Info("Tunnel 启动", "server", t.serverAddr, "device", t.deviceID)

	for {
		select {
		case <-ctx.Done():
			t.logger.Info("Tunnel 收到关闭信号")
			t.sessionMgr.DestroyAll()
			return ctx.Err()
		default:
		}

		// 尝试连接
		if err := t.connect(ctx); err != nil {
			t.logger.Error("连接失败", "error", err)
		}

		// 等待后重连
		delay := t.nextBackoff()
		t.logger.Info("等待后重连", "delay", delay)
		select {
		case <-ctx.Done():
			t.sessionMgr.DestroyAll()
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// connect 建立隧道连接并进入 ACTIVE 状态。
// 成功则阻塞在消息循环上直到断开，失败则返回 error。
func (t *Tunnel) connect(ctx context.Context) error {
	t.setState(StateConnecting)

	// 通过 Connect-RPC 建立双向流
	stream := t.client.Tunnel(ctx)

	// 第一步：发送 AuthRequest
	authReq := &fishttyv1.TunnelMessage{
		Payload: &fishttyv1.TunnelMessage_AuthReq{
			AuthReq: &fishttyv1.AuthRequest{
				DeviceId:     t.deviceID,
				Token:        t.token,
				AgentVersion: t.agentVer,
				Hostname:     t.hostname,
				Platform:     t.platform,
			},
		},
	}
	if err := stream.Send(authReq); err != nil {
		return fmt.Errorf("发送 AuthRequest 失败: %w", err)
	}
	t.logger.Info("已发送 AuthRequest")

	// 等待 AuthResponse
	authResp, err := stream.Receive()
	if err != nil {
		return fmt.Errorf("接收 AuthResponse 失败: %w", err)
	}

	authPayload, ok := authResp.Payload.(*fishttyv1.TunnelMessage_AuthResp)
	if !ok {
		return fmt.Errorf("期望 AuthResponse，收到 %T", authResp.Payload)
	}

	resp := authPayload.AuthResp
	if resp.Status != fishttyv1.AuthStatus_AUTH_STATUS_OK {
		return fmt.Errorf("认证失败: %s (code=%v)", resp.Message, resp.Status)
	}

	t.logger.Info("认证成功", "tunnel_id", resp.TunnelId)
	t.setState(StateActive)

	// 启动后台任务
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup

	// sendLoop：从 sendCh 读取消息并发送到 stream
	wg.Add(1)
	go func() {
		defer wg.Done()
		t.sendLoop(ctx, stream)
	}()

	// heartbeat：每 15s 发送心跳
	wg.Add(1)
	go func() {
		defer wg.Done()
		t.heartbeatLoop(ctx)
	}()

	// recvLoop：从 stream 接收消息（放入 goroutine，主线程等 ctx 取消）
	var recvErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		recvErr = t.recvLoop(ctx, stream)
	}()

	// 等待 ctx 取消（优雅关闭）或 recvLoop 自然退出（连接断开）
	activeStart := time.Now()
	<-ctx.Done()

	// 主动掐断 HTTP/2 流，让 recvLoop 的 stream.Receive() 立即返回
	stream.CloseRequest()
	cancel()
	wg.Wait()
	activeDuration := time.Since(activeStart)

	t.setState(StateReconnecting)

	// 如果连接持续时间足够长，重置退避
	if activeDuration >= reconnectResetAfter {
		t.resetBackoff()
	}

	if recvErr != nil && recvErr != context.Canceled {
		t.logger.Warn("隧道断开", "error", recvErr, "active_duration", activeDuration)
	} else {
		t.logger.Info("隧道正常关闭", "active_duration", activeDuration)
	}

	return recvErr
}

// sendLoop 从 sendCh 读取消息并通过 stream 发送。
func (t *Tunnel) sendLoop(ctx context.Context, stream *connect.BidiStreamForClient[fishttyv1.TunnelMessage, fishttyv1.TunnelMessage]) {
	defer func() {
		if r := recover(); r != nil {
			t.logger.Error("sendLoop panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-t.sendCh:
			if err := stream.Send(msg); err != nil {
				t.logger.Error("发送消息失败，触发重连",
					"error", err, "session_id", msg.SessionId,
					"type", fmt.Sprintf("%T", msg.Payload))
				t.signalReconnect()
				return
			}
		}
	}
}

// recvLoop 从 stream 接收消息并分发到 handler。
// 阻塞直到收到错误或 ctx 取消。
func (t *Tunnel) recvLoop(ctx context.Context, stream *connect.BidiStreamForClient[fishttyv1.TunnelMessage, fishttyv1.TunnelMessage]) error {
	defer func() {
		if r := recover(); r != nil {
			t.logger.Error("recvLoop panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	for {
		msg, err := stream.Receive()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("接收消息失败: %w", err)
		}

		// 特殊处理：HeartbeatAck 由心跳 loop 消费
		if _, isAck := msg.Payload.(*fishttyv1.TunnelMessage_HeartbeatAck); isAck {
			select {
			case t.heartbeatAckCh <- msg.GetHeartbeatAck().Timestamp:
			default:
			}
			continue
		}

		// 其他消息交由 handler 处理
		t.handler.Handle(msg)
	}
}

// heartbeatLoop 每 15 秒发送心跳，并监控 ACK。
func (t *Tunnel) heartbeatLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			t.logger.Error("heartbeatLoop panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	missed := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ts := time.Now().UnixMilli()
			t.sendCh <- &fishttyv1.TunnelMessage{
				Payload: &fishttyv1.TunnelMessage_Heartbeat{
					Heartbeat: &fishttyv1.Heartbeat{Timestamp: ts},
				},
			}
			t.logger.Debug("发送心跳", "timestamp", ts)
			missed++
		case ackTs := <-t.heartbeatAckCh:
			t.logger.Debug("收到心跳 ACK", "timestamp", ackTs)
			missed = 0 // 重置连续未响应计数
		}

		// 检查是否连续多次未收到 ACK
		if missed >= heartbeatMissThreshold {
			t.logger.Warn("心跳超时", "missed", missed)
			t.signalReconnect()
			return
		}
	}
}

// signalReconnect 通知主循环触发重连。
func (t *Tunnel) signalReconnect() {
	select {
	case t.reconnectCh <- struct{}{}:
	default:
		// 已有一个重连信号在等待
	}
}

// ── 指数退避重连 ──

var (
	reconnectMu      sync.Mutex
	currentBackoff   = reconnectMinDelay
)

func (t *Tunnel) nextBackoff() time.Duration {
	reconnectMu.Lock()
	defer reconnectMu.Unlock()
	d := currentBackoff
	currentBackoff *= 2
	if currentBackoff > reconnectMaxDelay {
		currentBackoff = reconnectMaxDelay
	}
	return d
}

func (t *Tunnel) resetBackoff() {
	reconnectMu.Lock()
	defer reconnectMu.Unlock()
	currentBackoff = reconnectMinDelay
	t.logger.Debug("退避已重置")
}

// ── 状态管理 ──

func (t *Tunnel) setState(s TunnelState) {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	if t.state != s {
		t.logger.Info("状态变更", "from", t.state.String(), "to", s.String())
		t.state = s
	}
}

// State 返回当前隧道状态。
func (t *Tunnel) State() TunnelState {
	t.stateMu.RLock()
	defer t.stateMu.RUnlock()
	return t.state
}

// SessionManager 返回 SessionManager 引用（供外部使用）。
func (t *Tunnel) SessionManager() *SessionManager {
	return t.sessionMgr
}

// ── 工具函数 ──

// DefaultHostname 返回系统主机名，失败时返回 "unknown"。
func DefaultHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

// buildHTTPClient 创建支持 h2c（HTTP/2 Cleartext）的 HTTP 客户端。
// 当 serverAddr 以 http:// 开头时，使用 HTTP/2 Cleartext 传输层；
// 以 https:// 开头时使用标准 TLS + HTTP/2。
func buildHTTPClient(serverAddr string) *http.Client {
	if strings.HasPrefix(serverAddr, "https://") {
		// HTTPS：标准 http.Client 原生支持 HTTP/2 over TLS
		return &http.Client{}
	}

	// HTTP (h2c)：需要显式配置 HTTP/2 非加密传输
	// http2.Transport 在 AllowHTTP=true 时发送 HTTP/2 升级请求
	transport := &http2.Transport{
		AllowHTTP: true,
		// DialTLSContext 被 http2.Transport 在非 TLS 模式下调用。
		// 这里直接拨号 TCP，不做 TLS 握手。
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{Transport: transport}
}
