package service

import (
	"context"
	"io"
	"log/slog"
	"runtime/debug"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	"github.com/frozenf1sh/fishpts/internal/domain"
)

// 编译时验证
var _ domain.Session = (*session)(nil)

// session 是 domain.Session 的具体实现。
// 每个 session 拥有独立的 PTY、输出缓冲区、序号计数器和一个 readLoop goroutine。
type session struct {
	id             string
	term           domain.TerminalEmulator
	buf            domain.OutputBuffer
	seq            uint64
	pendingEchoSeq uint32 // 待回传的本地回显序号（来自 Mobile stdin）
	sender         MessageSender
	ctx            context.Context
	cancel         context.CancelFunc
	logger         *slog.Logger
}

func newSession(id string, term domain.TerminalEmulator, buf domain.OutputBuffer, sender MessageSender, logger *slog.Logger) *session {
	ctx, cancel := context.WithCancel(context.Background())
	return &session{
		id:     id, term: term, buf: buf, sender: sender,
		ctx:    ctx, cancel: cancel,
		logger: logger.With("session_id", id),
	}
}

// start 启动 readLoop goroutine。
func (s *session) start() {
	go s.readLoop()
	s.logger.Info("session 已启动")
}

// ── domain.Session 实现 ──

func (s *session) ID() string               { return s.id }
func (s *session) WriteStdin(data []byte) error { _, err := s.term.Write(data); return err }
func (s *session) Resize(cols, rows uint32) error { return s.term.Resize(cols, rows) }

func (s *session) ReplayFrom(lastSeq uint64) *fishttyv1.ReattachData {
	chunks, startSeq := s.buf.ReplayFrom(lastSeq)
	return &fishttyv1.ReattachData{SessionId: s.id, StartSeq: startSeq, Chunks: chunks}
}

// SetPendingEchoSeq 记录 Mobile 端的本地回显序号，供下一次 PTY 输出时回传。
func (s *session) SetPendingEchoSeq(seq uint32) { s.pendingEchoSeq = seq }

// GetAndClearPendingEchoSeq 取出并清空待回传的本地回显序号。
func (s *session) GetAndClearPendingEchoSeq() uint32 {
	v := s.pendingEchoSeq
	s.pendingEchoSeq = 0
	return v
}

func (s *session) Destroy() {
	s.logger.Info("正在销毁 session")
	s.cancel()
	if err := s.term.Close(); err != nil { s.logger.Warn("PTY 关闭出错", "error", err) }
	s.logger.Info("session 已销毁")
}

// ── readLoop ──

func (s *session) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("readLoop panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	defer s.logger.Info("readLoop 退出")

	buf := make([]byte, 32768)
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		n, err := s.term.Read(buf)
		if n > 0 {
			s.seq++
			seq := s.seq
			_ = s.buf.Append(seq, buf[:n])

			data := make([]byte, n)
			copy(data, buf[:n])

			s.sender.SendMessage(&fishttyv1.TunnelMessage{
				SessionId: s.id,
				Payload: &fishttyv1.TunnelMessage_DataChunk{DataChunk: &fishttyv1.DataChunk{
					Seq: seq, Data: data,
					EchoSeq: s.GetAndClearPendingEchoSeq(),
				}},
			})
		}
		if err != nil {
			if err != io.EOF { s.logger.Error("PTY 读取错误", "error", err) }
			s.sendDestroyed()
			return
		}
	}
}

func (s *session) sendDestroyed() {
	s.sender.SendMessage(&fishttyv1.TunnelMessage{
		SessionId: s.id,
		Payload:   &fishttyv1.TunnelMessage_SessionDestroyed{SessionDestroyed: &fishttyv1.SessionDestroyed{SessionId: s.id}},
	})
}
