package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"connectrpc.com/connect"
	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	fishttyv1connect "github.com/frozenf1sh/fishpts/gen/fishtty/v1/fishttyv1connect"
)

// ── TunnelHandler ──
//
// 实现 Connect-RPC 的 FishTTYHandler 接口。
// 每个 Agent 连接会创建一个 TunnelHandler 实例，
// 在其 Tunnel() 方法中管理整个双向流的生命周期。

// TunnelHandler 处理单个 Agent 的 Connect-RPC 双向流隧道。
type TunnelHandler struct {
	devices *DeviceRegistry
	relay   *Relay
	logger  *slog.Logger
}

// NewTunnelHandler 创建 TunnelHandler。
func NewTunnelHandler(devices *DeviceRegistry, relay *Relay) *TunnelHandler {
	return &TunnelHandler{
		devices: devices,
		relay:   relay,
		logger:  slog.Default().With("component", "tunnel_handler"),
	}
}

// Handler 返回 Connect-RPC HTTP handler。
func (h *TunnelHandler) Handler() (string, http.Handler) {
	return fishttyv1connect.NewFishTTYHandler(h, connect.WithHandlerOptions())
}

// Tunnel 实现 FishTTYHandler.Tunnel。
func (h *TunnelHandler) Tunnel(
	ctx context.Context,
	stream *connect.BidiStream[fishttyv1.TunnelMessage, fishttyv1.TunnelMessage],
) error {
	// ── 第一步：等待 AuthRequest ──
	msg, err := stream.Receive()
	if err != nil {
		return fmt.Errorf("等待 AuthRequest 失败: %w", err)
	}

	authReq, ok := msg.Payload.(*fishttyv1.TunnelMessage_AuthReq)
	if !ok {
		h.sendError(stream, "", fishttyv1.ErrorCode_ERROR_CODE_UNAUTHORIZED, "首条消息必须是 AuthRequest")
		return fmt.Errorf("首条消息不是 AuthRequest，收到 %T", msg.Payload)
	}

	h.logger.Info("收到 AuthRequest",
		"device_id", authReq.AuthReq.DeviceId,
		"hostname", authReq.AuthReq.Hostname,
	)

	// ── 第二步：认证 ──
	authResp, err := AuthenticateAgent(h.devices, authReq.AuthReq)
	if err != nil {
		h.sendAuthResponse(stream, authResp)
		return fmt.Errorf("认证失败: %w", err)
	}

	if err := h.sendAuthResponse(stream, authResp); err != nil {
		return fmt.Errorf("发送 AuthResponse 失败: %w", err)
	}

	deviceID := authReq.AuthReq.DeviceId
	tunnelLogger := h.logger.With("device_id", deviceID)
	tunnelLogger.Info("Agent 认证成功，隧道已建立")

	// ── 第三步：注册到 Relay ──
	sendCh := make(chan *fishttyv1.TunnelMessage, 256)
	h.relay.RegisterAgent(deviceID, sendCh)
	defer h.relay.UnregisterAgent(deviceID)

	// ── 第四步：启动 sendLoop + recvLoop ──
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// sendLoop：从 sendCh 读取 Relay 转发的消息，通过 stream 发送给 Agent
	go h.sendLoop(ctx, stream, sendCh, cancel, tunnelLogger)

	// recvLoop：从 stream 接收 Agent 消息，交给 Relay 路由
	return h.recvLoop(ctx, stream, deviceID, cancel, tunnelLogger)
}

// sendLoop 从 sendCh 读取消息并发送到 Agent 的 BidiStream。
// 遇到 Send 错误时取消 ctx（触发 recvLoop 退出并重连），而非永久退出。
func (h *TunnelHandler) sendLoop(
	ctx context.Context,
	stream *connect.BidiStream[fishttyv1.TunnelMessage, fishttyv1.TunnelMessage],
	sendCh <-chan *fishttyv1.TunnelMessage,
	cancel context.CancelFunc,
	logger *slog.Logger,
) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("sendLoop panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-sendCh:
			if !ok {
				return
			}
			if err := stream.Send(msg); err != nil {
				logger.Error("发送消息到 Agent 失败，触发重连", "error", err,
					"session_id", msg.SessionId, "type", fmt.Sprintf("%T", msg.Payload))
				cancel() // 通知 recvLoop 退出，触发 Agent 重连
				return
			}
		}
	}
}

// recvLoop 从 Agent 的 BidiStream 接收消息并交给 Relay 路由。
func (h *TunnelHandler) recvLoop(
	ctx context.Context,
	stream *connect.BidiStream[fishttyv1.TunnelMessage, fishttyv1.TunnelMessage],
	deviceID string,
	cancel context.CancelFunc,
	logger *slog.Logger,
) error {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("recvLoop panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()

	for {
		msg, err := stream.Receive()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logger.Warn("接收 Agent 消息失败，触发重连", "error", err)
			cancel()
			return err
		}

		// Heartbeat → 立即回复 HeartbeatAck
		if heartbeat, isHB := msg.Payload.(*fishttyv1.TunnelMessage_Heartbeat); isHB {
			ack := &fishttyv1.TunnelMessage{
				Payload: &fishttyv1.TunnelMessage_HeartbeatAck{
					HeartbeatAck: &fishttyv1.HeartbeatAck{
						Timestamp: heartbeat.Heartbeat.Timestamp,
					},
				},
			}
			if err := stream.Send(ack); err != nil {
				logger.Error("发送 HeartbeatAck 失败，触发重连", "error", err)
				cancel()
				return err
			}
			h.devices.UpdateHeartbeat(deviceID)
			continue
		}

		// 其他消息：交给 Relay 路由给 Mobile
		h.relay.RouteFromAgent(deviceID, msg)
	}
}

// ── 辅助方法 ──

func (h *TunnelHandler) sendAuthResponse(
	stream *connect.BidiStream[fishttyv1.TunnelMessage, fishttyv1.TunnelMessage],
	resp *fishttyv1.AuthResponse,
) error {
	return stream.Send(&fishttyv1.TunnelMessage{
		Payload: &fishttyv1.TunnelMessage_AuthResp{
			AuthResp: resp,
		},
	})
}

func (h *TunnelHandler) sendError(
	stream *connect.BidiStream[fishttyv1.TunnelMessage, fishttyv1.TunnelMessage],
	sid string,
	code fishttyv1.ErrorCode,
	message string,
) {
	_ = stream.Send(&fishttyv1.TunnelMessage{
		SessionId: sid,
		Payload: &fishttyv1.TunnelMessage_ErrorMsg{
			ErrorMsg: &fishttyv1.ErrorMsg{
				SessionId: sid,
				Code:      code,
				Message:   message,
			},
		},
	})
}
