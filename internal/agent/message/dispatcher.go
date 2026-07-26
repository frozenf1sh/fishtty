// Package message 提供 Agent 端消息分发器。
// 将来自 Server 的 TunnelMessage 路由到正确的 Session 或 SessionManager。
package message

import (
	"fmt"
	"log/slog"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	"github.com/frozenf1sh/fishpts/internal/domain"
)

// MessageSender 接口（仅需要 SendMessage，符合 ISP）。
type MessageSender interface {
	SendMessage(msg *fishttyv1.TunnelMessage) error
}

// Dispatcher 实现 domain.MessageDispatcher。
// 以 TunnelMessage.session_id 为唯一路由 Key。
type Dispatcher struct {
	sessions domain.SessionManager
	sender   MessageSender
	logger   *slog.Logger
}

// NewDispatcher 创建消息分发器。
func NewDispatcher(sessions domain.SessionManager, sender MessageSender, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{sessions: sessions, sender: sender, logger: logger}
}

// ── domain.MessageDispatcher 实现 ──

// Dispatch 根据消息类型路由到对应处理器。
func (d *Dispatcher) Dispatch(msg *fishttyv1.TunnelMessage) {
	defer d.recoverPanic(msg)

	sid := msg.SessionId
	switch p := msg.Payload.(type) {
	case *fishttyv1.TunnelMessage_SessionInit:
		d.handleSessionInit(sid, p.SessionInit)
	case *fishttyv1.TunnelMessage_SessionDestroy:
		d.handleSessionDestroy(sid)
	case *fishttyv1.TunnelMessage_DataChunk:
		d.handleDataChunk(sid, p.DataChunk)
	case *fishttyv1.TunnelMessage_Resize:
		d.handleResize(sid, p.Resize)
	case *fishttyv1.TunnelMessage_Reattach:
		d.handleReattach(sid, p.Reattach)
	case *fishttyv1.TunnelMessage_HeartbeatAck:
		// 由 TunnelService 层通过 channel 处理，这里忽略
		d.logger.Debug("收到 HeartbeatAck（TunnelService 处理）")
	default:
		d.logger.Warn("收到未知消息类型", "type", fmt.Sprintf("%T", p))
	}
}

// ── 消息处理 ──

func (d *Dispatcher) handleSessionInit(sid string, init *fishttyv1.SessionInit) {
	d.logger.Info("收到 SessionInit", "session_id", sid)
	created, err := d.sessions.Create(init)
	if err != nil {
		d.sendError(sid, fishttyv1.ErrorCode_ERROR_CODE_COMMAND_FAILED, err.Error())
		return
	}
	d.sendMsg(&fishttyv1.TunnelMessage{
		SessionId: sid,
		Payload:   &fishttyv1.TunnelMessage_SessionCreated{SessionCreated: created},
	})
}

func (d *Dispatcher) handleSessionDestroy(sid string) {
	d.logger.Info("收到 SessionDestroy", "session_id", sid)
	if err := d.sessions.Destroy(sid); err != nil {
		d.sendError(sid, fishttyv1.ErrorCode_ERROR_CODE_SESSION_NOT_FOUND, err.Error())
	}
}

func (d *Dispatcher) handleDataChunk(sid string, chunk *fishttyv1.DataChunk) {
	s, ok := d.sessions.Get(sid)
	if !ok {
		d.sendError(sid, fishttyv1.ErrorCode_ERROR_CODE_SESSION_NOT_FOUND, "session 不存在")
		return
	}
	if err := s.WriteStdin(chunk.Data); err != nil {
		d.logger.Error("写入 PTY 失败", "session_id", sid, "error", err)
	}
}

func (d *Dispatcher) handleResize(sid string, resize *fishttyv1.Resize) {
	s, ok := d.sessions.Get(sid)
	if !ok {
		d.sendError(sid, fishttyv1.ErrorCode_ERROR_CODE_SESSION_NOT_FOUND, "session 不存在")
		return
	}
	if err := s.Resize(resize.Cols, resize.Rows); err != nil {
		d.logger.Error("resize 失败", "session_id", sid, "error", err)
	}
}

func (d *Dispatcher) handleReattach(sid string, r *fishttyv1.Reattach) {
	d.logger.Info("收到 Reattach", "session_id", sid, "last_ack_seq", r.LastAckSeq)
	s, ok := d.sessions.Get(sid)
	if !ok {
		d.sendError(sid, fishttyv1.ErrorCode_ERROR_CODE_SESSION_NOT_FOUND, "session 不存在")
		return
	}
	data := s.ReplayFrom(r.LastAckSeq)
	d.sendMsg(&fishttyv1.TunnelMessage{
		SessionId: sid,
		Payload:   &fishttyv1.TunnelMessage_ReattachData{ReattachData: data},
	})
	d.logger.Info("Reattach 完成", "session_id", sid, "start_seq", data.StartSeq, "chunks", len(data.Chunks))
}

// ── 辅助 ──

func (d *Dispatcher) sendMsg(msg *fishttyv1.TunnelMessage) {
	if err := d.sender.SendMessage(msg); err != nil {
		d.logger.Error("发送消息失败", "error", err, "sid", msg.SessionId)
	}
}

func (d *Dispatcher) sendError(sid string, code fishttyv1.ErrorCode, message string) {
	d.logger.Warn("发送错误", "session_id", sid, "code", code, "message", message)
	d.sendMsg(&fishttyv1.TunnelMessage{
		SessionId: sid,
		Payload:   &fishttyv1.TunnelMessage_ErrorMsg{ErrorMsg: &fishttyv1.ErrorMsg{SessionId: sid, Code: code, Message: message}},
	})
}

func (d *Dispatcher) recoverPanic(msg *fishttyv1.TunnelMessage) {
	if r := recover(); r != nil {
		d.logger.Error("Dispatch panic", "panic", r, "session_id", msg.SessionId, "type", fmt.Sprintf("%T", msg.Payload))
	}
}
