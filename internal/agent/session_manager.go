package agent

import (
	"fmt"
	"log/slog"
	"sync"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
)

// SessionManager 管理所有活跃的 PTY 会话。
// 负责会话的创建、查找、销毁和生命周期管理。
// 所有方法都是并发安全的。
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	sendCh   chan<- *fishttyv1.TunnelMessage
	logger   *slog.Logger
}

// NewSessionManager 创建一个新的 SessionManager。
// sendCh 是通往 Tunnel sendLoop 的通道，用于发送消息给 Server。
func NewSessionManager(sendCh chan<- *fishttyv1.TunnelMessage, logger *slog.Logger) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
		sendCh:   sendCh,
		logger:   logger,
	}
}

// Create 根据 SessionInit 请求创建新的 PTY 会话。
// session_id 由发起方（Server/前端）预先生成并填入 SessionInit。
func (sm *SessionManager) Create(init *fishttyv1.SessionInit) (*fishttyv1.SessionCreated, error) {
	sid := init.SessionId
	if sid == "" {
		return nil, fmt.Errorf("session_id 不能为空")
	}

	// 检查是否已存在同 ID 的会话
	sm.mu.RLock()
	if _, exists := sm.sessions[sid]; exists {
		sm.mu.RUnlock()
		return nil, fmt.Errorf("session %s 已存在", sid)
	}
	sm.mu.RUnlock()

	// 创建 PTY
	ptySession, err := NewPty(PtyConfig{
		Command: init.Command,
		Cols:    init.Cols,
		Rows:    init.Rows,
		Env:     init.Env,
		WorkDir: init.WorkDir,
	})
	if err != nil {
		return &fishttyv1.SessionCreated{
			SessionId: sid,
			Status:    fishttyv1.SessionStatus_SESSION_STATUS_FAILED,
			Message:   err.Error(),
		}, err
	}

	// 创建环形缓冲区
	ringBuf := NewRingBuffer()

	// 创建 Session
	sessionLogger := sm.logger.With("session_id", sid)
	session := NewSession(sid, ptySession, ringBuf, sm.sendCh, sessionLogger)

	// 注册
	sm.mu.Lock()
	sm.sessions[sid] = session
	sm.mu.Unlock()

	// 启动 readLoop
	session.Start()

	sm.logger.Info("会话已创建",
		"session_id", sid,
		"command", init.Command,
		"pid", ptySession.Pid(),
	)

	return &fishttyv1.SessionCreated{
		SessionId: sid,
		Status:    fishttyv1.SessionStatus_SESSION_STATUS_OK,
	}, nil
}

// Get 根据 session_id 查找会话。
// 返回会话和是否存在的布尔值。
func (sm *SessionManager) Get(sid string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.sessions[sid]
	return s, ok
}

// Destroy 销毁指定会话：关闭 PTY、释放环形缓冲区、清理 goroutine。
// 成功后内部通过 Session.sendDestroyed() 发送确认消息给 Server。
func (sm *SessionManager) Destroy(sid string) error {
	sm.mu.Lock()
	session, ok := sm.sessions[sid]
	if !ok {
		sm.mu.Unlock()
		return fmt.Errorf("session %s 不存在", sid)
	}
	delete(sm.sessions, sid)
	sm.mu.Unlock()

	session.Destroy()
	sm.logger.Info("会话已销毁", "session_id", sid)
	return nil
}

// DestroyAll 销毁所有活跃会话。在 Agent 关闭时调用。
func (sm *SessionManager) DestroyAll() {
	sm.mu.Lock()
	sessions := make([]*Session, 0, len(sm.sessions))
	for sid, s := range sm.sessions {
		sessions = append(sessions, s)
		delete(sm.sessions, sid)
	}
	sm.mu.Unlock()

	for _, s := range sessions {
		s.Destroy()
	}
	sm.logger.Info("所有会话已销毁", "count", len(sessions))
}

// Count 返回当前活跃会话数量。
func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// IDs 返回所有活跃会话的 session_id 列表。
func (sm *SessionManager) IDs() []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	ids := make([]string, 0, len(sm.sessions))
	for id := range sm.sessions {
		ids = append(ids, id)
	}
	return ids
}
