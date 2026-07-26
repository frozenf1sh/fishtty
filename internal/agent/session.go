package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
)

// Session 表示一个活跃的 PTY 终端会话。
// 每个 Session 拥有自己的 PTY、环形缓冲区、序列号计数器和两个 goroutine。
type Session struct {
	id      string        // 会话唯一标识（由 Server/前端 生成）
	pty     *PtySession   // PTY 实例
	ringBuf *RingBuffer   // 128 KB 环形缓冲区（存储 stdout 历史）
	seq     uint64        // 当前序列号（Atomic 访问通过 channel 串行化）

	sendCh chan<- *fishttyv1.TunnelMessage // 发送通道（写入 Tunnel sendLoop）
	ctx    context.Context
	cancel context.CancelFunc

	logger *slog.Logger
}

// NewSession 创建一个新的 Session 实例。
// pty 必须已经创建好并且命令已在运行。
func NewSession(
	id string,
	pty *PtySession,
	ringBuf *RingBuffer,
	sendCh chan<- *fishttyv1.TunnelMessage,
	logger *slog.Logger,
) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{
		id:      id,
		pty:     pty,
		ringBuf: ringBuf,
		seq:     0,
		sendCh:  sendCh,
		ctx:     ctx,
		cancel:  cancel,
		logger:  logger.With("session_id", id),
	}
}

// Start 启动 Session 的 readLoop goroutine。
// readLoop 从 PTY 读取输出，写入环形缓冲区，并发送到 Server。
func (s *Session) Start() {
	go s.readLoop()
	s.logger.Info("session 已启动")
}

// readLoop 持续从 PTY master fd 读取输出。
// 阻塞在 Read 上，有数据时立刻转发。PTY 关闭或 ctx 取消时退出。
func (s *Session) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("readLoop panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	defer s.logger.Info("readLoop 退出")

	buf := make([]byte, 4096)
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		n, err := s.pty.Read(buf)
		if n > 0 {
			s.seq++
			seq := s.seq

			// 写入环形缓冲区
			_ = s.ringBuf.Write(seq, buf[:n])

			// 构造 DataChunk 并发送
			data := make([]byte, n)
			copy(data, buf[:n])

			msg := &fishttyv1.TunnelMessage{
				SessionId: s.id,
				Payload: &fishttyv1.TunnelMessage_DataChunk{
					DataChunk: &fishttyv1.DataChunk{
						Seq:  seq,
						Data: data,
					},
				},
			}

			select {
			case s.sendCh <- msg:
			case <-s.ctx.Done():
				return
			}
		}

		if err != nil {
			if err != io.EOF {
				s.logger.Error("PTY 读取错误", "error", err)
			}
			// PTY 已关闭 → 通知 Server 会话已结束
			s.sendDestroyed()
			return
		}
	}
}

// sendDestroyed 发送 SessionDestroyed 消息给 Server。
func (s *Session) sendDestroyed() {
	msg := &fishttyv1.TunnelMessage{
		SessionId: s.id,
		Payload: &fishttyv1.TunnelMessage_SessionDestroyed{
			SessionDestroyed: &fishttyv1.SessionDestroyed{
				SessionId: s.id,
			},
		},
	}
	select {
	case s.sendCh <- msg:
	case <-s.ctx.Done():
	}
}

// WriteData 向 PTY 写入数据（来自 Server 的 stdin）。
// 由 handler 在收到 DataChunk 时调用。
func (s *Session) WriteData(data []byte) error {
	select {
	case <-s.ctx.Done():
		return fmt.Errorf("session %s 已关闭", s.id)
	default:
	}

	_, err := s.pty.Write(data)
	if err != nil {
		return fmt.Errorf("pty write: %w", err)
	}
	return nil
}

// Resize 调整 PTY 窗口大小。
func (s *Session) Resize(cols, rows uint32) error {
	select {
	case <-s.ctx.Done():
		return fmt.Errorf("session %s 已关闭", s.id)
	default:
	}

	if err := s.pty.Resize(cols, rows); err != nil {
		return fmt.Errorf("resize: %w", err)
	}
	s.logger.Debug("resize", "cols", cols, "rows", rows)
	return nil
}

// Reattach 根据 lastAckSeq 从环形缓冲区中增量重放历史数据。
// 返回 ReattachData 消息（可能为空，如果无新数据）。
func (s *Session) Reattach(lastAckSeq uint64) *fishttyv1.ReattachData {
	chunks, startSeq := s.ringBuf.ReadFrom(lastAckSeq)

	return &fishttyv1.ReattachData{
		SessionId: s.id,
		StartSeq:  startSeq,
		Chunks:    chunks,
	}
}

// Destroy 关闭 Session：取消 context、关闭 PTY、释放环形缓冲区。
// 此方法会先尝试优雅关闭（SIGHUP），若超时则强制 SIGKILL。
func (s *Session) Destroy() {
	s.logger.Info("正在销毁 session")
	s.cancel() // 先取消 context，停止 readLoop

	// 关闭 PTY（关闭 master fd → 子进程收到 SIGHUP）
	if err := s.pty.Close(); err != nil {
		s.logger.Warn("PTY 关闭时出错", "error", err)
	}

	s.logger.Info("session 已销毁")
}

// ID 返回会话 ID。
func (s *Session) ID() string {
	return s.id
}
