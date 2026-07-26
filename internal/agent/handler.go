package agent

import (
	"fmt"
	"log/slog"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
)

// Handler 负责将从 Server 收到的 TunnelMessage 分发到正确的 Session
// 或 SessionManager。以外层 TunnelMessage.session_id 作为唯一路由 Key。
type Handler struct {
	sessionMgr *SessionManager
	sendCh     chan<- *fishttyv1.TunnelMessage
	logger     *slog.Logger
}

// NewHandler 创建消息分发器。
func NewHandler(sessionMgr *SessionManager, sendCh chan<- *fishttyv1.TunnelMessage, logger *slog.Logger) *Handler {
	return &Handler{
		sessionMgr: sessionMgr,
		sendCh:     sendCh,
		logger:     logger,
	}
}

// Handle 处理从 Server 收到的一条消息。
// 根据 session_id 和 payload 类型路由到对应逻辑。
func (h *Handler) Handle(msg *fishttyv1.TunnelMessage) {
	defer func() {
		if r := recover(); r != nil {
			h.logger.Error("handler panic",
				"panic", r,
				"session_id", msg.SessionId,
				"type", fmt.Sprintf("%T", msg.Payload),
			)
		}
	}()

	sid := msg.SessionId

	switch payload := msg.Payload.(type) {

	// ── 会话生命周期 ──
	case *fishttyv1.TunnelMessage_SessionInit:
		h.handleSessionInit(sid, payload.SessionInit)

	case *fishttyv1.TunnelMessage_SessionDestroy:
		h.handleSessionDestroy(sid, payload.SessionDestroy)

	// ── 数据面 ──
	case *fishttyv1.TunnelMessage_DataChunk:
		h.handleDataChunk(sid, payload.DataChunk)

	// ── 会话控制 ──
	case *fishttyv1.TunnelMessage_Resize:
		h.handleResize(sid, payload.Resize)

	// ── 重连 ──
	case *fishttyv1.TunnelMessage_Reattach:
		h.handleReattach(sid, payload.Reattach)

	// ── 健康检查 ──
	case *fishttyv1.TunnelMessage_HeartbeatAck:
		// HeartbeatAck 由 Tunnel 层直接处理（通过 channel 通知）
		// 这里不处理
		h.logger.Debug("收到 HeartbeatAck（由 Tunnel 处理）")

	// ── 未知消息类型 ──
	default:
		h.logger.Warn("收到未知消息类型", "type", fmt.Sprintf("%T", payload))
	}
}

// handleSessionInit 处理创建新会话的请求。
func (h *Handler) handleSessionInit(sid string, init *fishttyv1.SessionInit) {
	h.logger.Info("收到 SessionInit", "session_id", sid)

	created, err := h.sessionMgr.Create(init)
	if err != nil {
		h.sendError(sid, fishttyv1.ErrorCode_ERROR_CODE_COMMAND_FAILED, err.Error())
		return
	}

	// 发送 SessionCreated 回复
	h.sendCh <- &fishttyv1.TunnelMessage{
		SessionId: sid,
		Payload: &fishttyv1.TunnelMessage_SessionCreated{
			SessionCreated: created,
		},
	}
}

// handleSessionDestroy 处理销毁会话的请求。
func (h *Handler) handleSessionDestroy(sid string, _ *fishttyv1.SessionDestroy) {
	h.logger.Info("收到 SessionDestroy", "session_id", sid)

	if err := h.sessionMgr.Destroy(sid); err != nil {
		h.sendError(sid, fishttyv1.ErrorCode_ERROR_CODE_SESSION_NOT_FOUND, err.Error())
	}
	// 成功销毁时，session.Destroy() 内部会通过 sendDestroyed() 发送确认
}

// handleDataChunk 处理来自 Server 的 stdin 数据（用户输入）。
func (h *Handler) handleDataChunk(sid string, chunk *fishttyv1.DataChunk) {
	session, ok := h.sessionMgr.Get(sid)
	if !ok {
		h.sendError(sid, fishttyv1.ErrorCode_ERROR_CODE_SESSION_NOT_FOUND, "session 不存在")
		return
	}

	if err := session.WriteData(chunk.Data); err != nil {
		h.logger.Error("写入 PTY 失败", "session_id", sid, "error", err)
	}
}

// handleResize 处理终端尺寸调整请求。
func (h *Handler) handleResize(sid string, resize *fishttyv1.Resize) {
	session, ok := h.sessionMgr.Get(sid)
	if !ok {
		h.sendError(sid, fishttyv1.ErrorCode_ERROR_CODE_SESSION_NOT_FOUND, "session 不存在")
		return
	}

	if err := session.Resize(resize.Cols, resize.Rows); err != nil {
		h.logger.Error("resize 失败", "session_id", sid, "error", err)
	}
}

// handleReattach 处理重连请求：从环形缓冲区重放历史数据。
func (h *Handler) handleReattach(sid string, reattach *fishttyv1.Reattach) {
	h.logger.Info("收到 Reattach", "session_id", sid, "last_ack_seq", reattach.LastAckSeq)

	session, ok := h.sessionMgr.Get(sid)
	if !ok {
		h.sendError(sid, fishttyv1.ErrorCode_ERROR_CODE_SESSION_NOT_FOUND, "session 不存在")
		return
	}

	reattachData := session.Reattach(reattach.LastAckSeq)
	h.sendCh <- &fishttyv1.TunnelMessage{
		SessionId: sid,
		Payload: &fishttyv1.TunnelMessage_ReattachData{
			ReattachData: reattachData,
		},
	}
	h.logger.Info("Reattach 完成",
		"session_id", sid,
		"start_seq", reattachData.StartSeq,
		"chunks", len(reattachData.Chunks),
	)
}

// sendError 发送错误消息给 Server。
func (h *Handler) sendError(sid string, code fishttyv1.ErrorCode, message string) {
	h.logger.Warn("发送错误", "session_id", sid, "code", code, "message", message)
	h.sendCh <- &fishttyv1.TunnelMessage{
		SessionId: sid,
		Payload: &fishttyv1.TunnelMessage_ErrorMsg{
			ErrorMsg: &fishttyv1.ErrorMsg{
				SessionId: sid,
				Code:      code,
				Message:   message,
			},
		},
	}
}
