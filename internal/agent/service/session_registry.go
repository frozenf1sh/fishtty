// Package service 提供 Agent 应用服务层——会话管理与隧道连接编排。
package service

import (
	"fmt"
	"log/slog"
	"sync"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	"github.com/frozenf1sh/fishpts/internal/agent/adapter/ringbuf"
	"github.com/frozenf1sh/fishpts/internal/domain"
)

// 编译时验证
var _ domain.SessionManager = (*SessionRegistry)(nil)

// MessageSender 接口（ISP：只取 SendMessage）。
type MessageSender interface {
	SendMessage(msg *fishttyv1.TunnelMessage) error
}

// SessionRegistry 管理活跃的 PTY 会话，实现 domain.SessionManager。
// 负责创建、查找、销毁，所有方法并发安全。
type SessionRegistry struct {
	mu      sync.RWMutex
	items   map[string]*session
	factory domain.TerminalFactory
	sender  MessageSender
	logger  *slog.Logger
}

// NewSessionRegistry 创建会话注册表。
func NewSessionRegistry(factory domain.TerminalFactory, sender MessageSender, logger *slog.Logger) *SessionRegistry {
	return &SessionRegistry{
		items:   make(map[string]*session),
		factory: factory,
		sender:  sender,
		logger:  logger,
	}
}

// ── domain.SessionManager 实现 ──

// Create 根据 SessionInit 创建新的 PTY 会话。
func (sr *SessionRegistry) Create(init *fishttyv1.SessionInit) (*fishttyv1.SessionCreated, error) {
	sid := init.SessionId
	if sid == "" {
		return nil, fmt.Errorf("session_id 不能为空")
	}

	sr.mu.RLock()
	if _, exists := sr.items[sid]; exists {
		sr.mu.RUnlock()
		return nil, fmt.Errorf("session %s 已存在", sid)
	}
	sr.mu.RUnlock()

	// 创建 PTY
	term, err := sr.factory.Create(domain.TerminalConfig{
		Command: init.Command, Cols: init.Cols, Rows: init.Rows,
		Env: init.Env, WorkDir: init.WorkDir,
	})
	if err != nil {
		return &fishttyv1.SessionCreated{SessionId: sid, Status: fishttyv1.SessionStatus_SESSION_STATUS_FAILED, Message: err.Error()}, err
	}

	s := newSession(sid, term, ringbuf.New(), sr.sender, sr.logger)

	sr.mu.Lock()
	sr.items[sid] = s
	sr.mu.Unlock()

	s.start()
	sr.logger.Info("会话已创建", "session_id", sid, "command", init.Command, "pid", term.Pid())
	return &fishttyv1.SessionCreated{SessionId: sid, Status: fishttyv1.SessionStatus_SESSION_STATUS_OK}, nil
}

func (sr *SessionRegistry) Get(sid string) (domain.Session, bool) {
	sr.mu.RLock(); defer sr.mu.RUnlock()
	s, ok := sr.items[sid]
	return s, ok
}

func (sr *SessionRegistry) Destroy(sid string) error {
	sr.mu.Lock()
	s, ok := sr.items[sid]
	if !ok { sr.mu.Unlock(); return fmt.Errorf("session %s 不存在", sid) }
	delete(sr.items, sid)
	sr.mu.Unlock()
	s.Destroy()
	sr.logger.Info("会话已销毁", "session_id", sid)
	return nil
}

func (sr *SessionRegistry) DestroyAll() {
	sr.mu.Lock()
	list := make([]*session, 0, len(sr.items))
	for sid, s := range sr.items { list = append(list, s); delete(sr.items, sid) }
	sr.mu.Unlock()
	for _, s := range list { s.Destroy() }
	sr.logger.Info("所有会话已销毁", "count", len(list))
}

func (sr *SessionRegistry) Count() int { sr.mu.RLock(); defer sr.mu.RUnlock(); return len(sr.items) }
