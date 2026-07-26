// Package connectrpc 将 Connect-RPC 双向流适配为 Server 端 handler。
package connectrpc

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"connectrpc.com/connect"
	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	fishttyv1connect "github.com/frozenf1sh/fishpts/gen/fishtty/v1/fishttyv1connect"
	"github.com/frozenf1sh/fishpts/internal/domain"
	"github.com/frozenf1sh/fishpts/internal/server/auth"
)

// Handler 实现 Connect-RPC FishTTYHandler，管理 Agent 隧道的生命周期。
type Handler struct {
	devices domain.DeviceStore
	relay   domain.RelayRouter
	logger  *slog.Logger
}

// NewHandler 创建 Connect-RPC handler。
func NewHandler(devices domain.DeviceStore, relay domain.RelayRouter) *Handler {
	return &Handler{devices: devices, relay: relay, logger: slog.Default().With("component", "tunnel_handler")}
}

// Route 返回 Connect-RPC HTTP 挂载路径和 handler。
func (h *Handler) Route() (string, http.Handler) {
	return fishttyv1connect.NewFishTTYHandler(h, connect.WithHandlerOptions())
}

// Tunnel 实现 FishTTYHandler.Tunnel。
func (h *Handler) Tunnel(ctx context.Context, stream *connect.BidiStream[fishttyv1.TunnelMessage, fishttyv1.TunnelMessage]) error {
	msg, err := stream.Receive()
	if err != nil { return fmt.Errorf("等待 AuthRequest 失败: %w", err) }

	authReq, ok := msg.Payload.(*fishttyv1.TunnelMessage_AuthReq)
	if !ok {
		sendErr(stream, "", fishttyv1.ErrorCode_ERROR_CODE_UNAUTHORIZED, "首条消息必须是 AuthRequest")
		return fmt.Errorf("首条消息不是 AuthRequest")
	}

	h.logger.Info("收到 AuthRequest", "device_id", authReq.AuthReq.DeviceId)

	resp, err := auth.Agent(h.devices, authReq.AuthReq)
	if err != nil {
		_ = stream.Send(&fishttyv1.TunnelMessage{Payload: &fishttyv1.TunnelMessage_AuthResp{AuthResp: resp}})
		return fmt.Errorf("认证失败: %w", err)
	}
	if err := stream.Send(&fishttyv1.TunnelMessage{Payload: &fishttyv1.TunnelMessage_AuthResp{AuthResp: resp}}); err != nil {
		return fmt.Errorf("发送 AuthResponse 失败: %w", err)
	}

	deviceID := authReq.AuthReq.DeviceId
	logger := h.logger.With("device_id", deviceID)
	logger.Info("Agent 认证成功，隧道已建立")

	// 注册到中继：创建 channel → drainLoop
	ch := make(chan *fishttyv1.TunnelMessage, 256)
	sender := &streamSender{stream: stream, logger: logger}
	h.relay.RegisterAgent(deviceID, sender)
	defer h.relay.UnregisterAgent(deviceID)

	// drainLoop：从 channel 读消息 → stream.Send()
	go func() {
		defer func() { if r := recover(); r != nil { logger.Error("agentDrain panic", "panic", r) } }()
		for msg := range ch {
			if err := stream.Send(msg); err != nil {
				logger.Error("发送到 Agent 失败", "error", err); return
			}
		}
	}()

	// recvLoop：stream.Receive() → relay.RouteFromAgent
	for {
		msg, err := stream.Receive()
		if err != nil {
			if ctx.Err() != nil { return ctx.Err() }
			return err
		}
		if hb, ok := msg.Payload.(*fishttyv1.TunnelMessage_Heartbeat); ok {
			_ = stream.Send(&fishttyv1.TunnelMessage{
				Payload: &fishttyv1.TunnelMessage_HeartbeatAck{
					HeartbeatAck: &fishttyv1.HeartbeatAck{Timestamp: hb.Heartbeat.Timestamp},
				},
			})
			h.devices.UpdateHeartbeat(deviceID)
			continue
		}
		h.relay.RouteFromAgent(deviceID, msg)
	}
}

// streamSender 实现 domain.MessageSender，用 Connect-RPC stream 发消息。
type streamSender struct {
	stream *connect.BidiStream[fishttyv1.TunnelMessage, fishttyv1.TunnelMessage]
	logger *slog.Logger
}

func (s *streamSender) SendMessage(msg *fishttyv1.TunnelMessage) error {
	return s.stream.Send(msg)
}

func sendErr(stream *connect.BidiStream[fishttyv1.TunnelMessage, fishttyv1.TunnelMessage], sid string, code fishttyv1.ErrorCode, message string) {
	_ = stream.Send(&fishttyv1.TunnelMessage{
		SessionId: sid,
		Payload:   &fishttyv1.TunnelMessage_ErrorMsg{ErrorMsg: &fishttyv1.ErrorMsg{SessionId: sid, Code: code, Message: message}},
	})
}
