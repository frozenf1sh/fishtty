package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	fishttyv1connect "github.com/frozenf1sh/fishpts/gen/fishtty/v1/fishttyv1connect"
	"github.com/frozenf1sh/fishpts/internal/agent/adapter/connectrpc"
	"github.com/frozenf1sh/fishpts/internal/agent/adapter/pty"
	"github.com/frozenf1sh/fishpts/internal/agent/message"
	"github.com/frozenf1sh/fishpts/internal/config"
	"github.com/frozenf1sh/fishpts/internal/domain"
	"github.com/frozenf1sh/fishpts/pkg/backoff"
	"github.com/frozenf1sh/fishpts/pkg/h2c"
)

const sendChSize = 256

// ── TunnelService ──

// TunnelService 管理 Agent 到 Server 的 Connect-RPC 隧道生命周期。
type TunnelService struct {
	serverAddr      string
	deviceID        string
	token           string
	agentVer        string
	hostname        string
	platform        string
	httpClient      *http.Client
	rpcClient       fishttyv1connect.FishTTYClient
	heartbeatCfg    config.HeartbeatConfig

	sendCh         chan *fishttyv1.TunnelMessage
	heartbeatAckCh chan int64
	backoff        *backoff.Exponential

	dispatcher domain.MessageDispatcher
	sessions   domain.SessionManager
	logger     *slog.Logger
}

// Config 创建参数。
type Config struct {
	ServerAddr string
	DeviceID   string
	Token      string
	AgentVer   string
	Hostname   string
	Heartbeat  config.HeartbeatConfig
	Reconnect  config.ReconnectConfig
	RingBuffer config.RingBufferConfig
}

// NewTunnelService 创建隧道服务。
func NewTunnelService(cfg Config) *TunnelService {
	httpClient := h2c.NewClient(cfg.ServerAddr)
	rpcClient := fishttyv1connect.NewFishTTYClient(httpClient, cfg.ServerAddr)
	logger := slog.Default().With("component", "tunnel")

	ts := &TunnelService{
		serverAddr:     cfg.ServerAddr,
		deviceID:       cfg.DeviceID,
		token:          cfg.Token,
		agentVer:       cfg.AgentVer,
		hostname:       cfg.Hostname,
		platform:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		httpClient:     httpClient,
		rpcClient:      rpcClient,
		heartbeatCfg:   cfg.Heartbeat,
		sendCh:         make(chan *fishttyv1.TunnelMessage, sendChSize),
		heartbeatAckCh: make(chan int64, 16),
		backoff:        backoff.NewExponential(cfg.Reconnect.MinDelay, cfg.Reconnect.MaxDelay, cfg.Reconnect.ResetAfter),
		logger:         logger,
	}

	// 组装依赖链：sender → sessions → dispatcher
	sender := &tunnelSender{ch: ts.sendCh, logger: logger}
	ts.sessions = NewSessionRegistry(pty.NewFactory(), sender, logger)
	ts.dispatcher = message.NewDispatcher(ts.sessions, sender, logger)

	return ts
}

// Sessions 暴露 SessionManager（供外部查询）。
func (ts *TunnelService) Sessions() domain.SessionManager { return ts.sessions }

// DefaultHostname 返回系统主机名。
func DefaultHostname() string { h, _ := os.Hostname(); return h }

// ── 生命周期 ──

// Run 启动隧道主循环：连接 → 运行 → 重连 → 循环。阻塞直到 ctx 取消。
func (ts *TunnelService) Run(ctx context.Context) error {
	ts.logger.Info("Tunnel 启动", "server", ts.serverAddr, "device", ts.deviceID)
	for {
		select {
		case <-ctx.Done():
			ts.logger.Info("Tunnel 收到关闭信号")
			ts.sessions.DestroyAll()
			return ctx.Err()
		default:
		}

		if err := ts.connect(ctx); err != nil {
			ts.logger.Error("连接失败", "error", err)
		}

		delay := ts.backoff.Next()
		ts.logger.Info("等待后重连", "delay", delay)
		select {
		case <-ctx.Done():
			ts.sessions.DestroyAll()
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// connect 建立一次隧道连接，阻塞直到断开。
func (ts *TunnelService) connect(parentCtx context.Context) error {
	stream := ts.rpcClient.Tunnel(parentCtx)
	conn := connectrpc.NewStreamAdapter(stream)

	// 认证
	if err := ts.authenticate(conn); err != nil {
		return err
	}

	// 启动 goroutine
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1); go func() { defer wg.Done(); ts.sendLoop(ctx, conn) }()
	wg.Add(1); go func() { defer wg.Done(); ts.heartbeatLoop(ctx) }()

	var recvErr error
	wg.Add(1); go func() { defer wg.Done(); recvErr = ts.recvLoop(ctx, conn) }()

	activeStart := time.Now()
	<-ctx.Done()
	conn.CloseRequest()
	cancel()
	wg.Wait()
	activeDuration := time.Since(activeStart)

	ts.backoff.ResetIfStable()
	if recvErr != nil && recvErr != context.Canceled {
		ts.logger.Warn("隧道断开", "error", recvErr, "active_duration", activeDuration)
	} else {
		ts.logger.Info("隧道正常关闭", "active_duration", activeDuration)
	}
	return recvErr
}

// ── 认证 ──

func (ts *TunnelService) authenticate(conn domain.StreamConn) error {
	req := &fishttyv1.TunnelMessage{
		Payload: &fishttyv1.TunnelMessage_AuthReq{
			AuthReq: &fishttyv1.AuthRequest{
				DeviceId: ts.deviceID, Token: ts.token,
				AgentVersion: ts.agentVer, Hostname: ts.hostname, Platform: ts.platform,
			},
		},
	}
	if err := conn.SendMessage(req); err != nil { return fmt.Errorf("发送 AuthRequest 失败: %w", err) }
	ts.logger.Info("已发送 AuthRequest")

	resp, err := conn.ReceiveMessage()
	if err != nil { return fmt.Errorf("接收 AuthResponse 失败: %w", err) }

	auth, ok := resp.Payload.(*fishttyv1.TunnelMessage_AuthResp)
	if !ok { return fmt.Errorf("期望 AuthResponse，收到 %T", resp.Payload) }
	if auth.AuthResp.Status != fishttyv1.AuthStatus_AUTH_STATUS_OK {
		return fmt.Errorf("认证失败: %s", auth.AuthResp.Message)
	}
	ts.logger.Info("认证成功", "tunnel_id", auth.AuthResp.TunnelId)
	return nil
}

// ── goroutine 循环 ──

func (ts *TunnelService) sendLoop(ctx context.Context, conn domain.StreamConn) {
	defer ts.recoverLog("sendLoop")
	for {
		select {
		case <-ctx.Done(): return
		case msg := <-ts.sendCh:
			if err := conn.SendMessage(msg); err != nil {
				ts.logger.Error("发送消息失败", "error", err, "sid", msg.SessionId)
				return
			}
		}
	}
}

func (ts *TunnelService) recvLoop(ctx context.Context, conn domain.StreamConn) error {
	defer ts.recoverLog("recvLoop")
	for {
		msg, err := conn.ReceiveMessage()
		if err != nil {
			if ctx.Err() != nil { return ctx.Err() }
			return fmt.Errorf("接收消息失败: %w", err)
		}

		if ack, ok := msg.Payload.(*fishttyv1.TunnelMessage_HeartbeatAck); ok {
			select {
			case ts.heartbeatAckCh <- ack.HeartbeatAck.Timestamp:
			default:
			}
			continue
		}
		ts.dispatcher.Dispatch(msg)
	}
}

func (ts *TunnelService) heartbeatLoop(ctx context.Context) {
	defer ts.recoverLog("heartbeatLoop")
	ticker := time.NewTicker(ts.heartbeatCfg.Interval)
	defer ticker.Stop()
	missed := 0

	for {
		select {
		case <-ctx.Done(): return
		case <-ticker.C:
			ts.sendCh <- &fishttyv1.TunnelMessage{
				Payload: &fishttyv1.TunnelMessage_Heartbeat{Heartbeat: &fishttyv1.Heartbeat{Timestamp: time.Now().UnixMilli()}},
			}
			missed++
		case <-ts.heartbeatAckCh:
			missed = 0
		}
		if missed >= ts.heartbeatCfg.MissThreshold {
			ts.logger.Warn("心跳超时", "missed", missed)
			return
		}
	}
}

func (ts *TunnelService) recoverLog(name string) {
	if r := recover(); r != nil {
		ts.logger.Error(name+" panic", "panic", r, "stack", string(debug.Stack()))
	}
}

// ── tunnelSender ──

// tunnelSender 实现 message.MessageSender 接口，通过 channel 发消息。
type tunnelSender struct {
	ch     chan<- *fishttyv1.TunnelMessage
	logger *slog.Logger
}

func (s *tunnelSender) SendMessage(msg *fishttyv1.TunnelMessage) error {
	select {
	case s.ch <- msg: return nil
	default:
		s.logger.Warn("发送通道已满，丢弃消息", "sid", msg.SessionId)
		return fmt.Errorf("send channel full")
	}
}
