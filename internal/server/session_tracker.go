package server

import (
	"sync"
	"time"
)

// SessionInfo 描述一个活跃的 PTY 会话。
type SessionInfo struct {
	SessionID  string    // 会话唯一标识
	DeviceID   string    // 所属设备
	CreatedAt  time.Time // 创建时间
	LastActive time.Time // 最后活跃时间
}

// SessionTracker 跟踪所有活跃的 PTY 会话。
// 维护 session_id → SessionInfo 的映射，提供查询和统计功能。
// 与 Relay 配合使用：Relay 负责路由，SessionTracker 负责元数据。
type SessionTracker struct {
	mu       sync.RWMutex
	sessions map[string]*SessionInfo
}

// NewSessionTracker 创建新的 SessionTracker。
func NewSessionTracker() *SessionTracker {
	return &SessionTracker{
		sessions: make(map[string]*SessionInfo),
	}
}

// Track 记录一个新的活跃会话。
func (st *SessionTracker) Track(sessionID, deviceID string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.sessions[sessionID] = &SessionInfo{
		SessionID:  sessionID,
		DeviceID:   deviceID,
		CreatedAt:  time.Now(),
		LastActive: time.Now(),
	}
}

// Untrack 移除一个会话的记录。
func (st *SessionTracker) Untrack(sessionID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.sessions, sessionID)
}

// Touch 更新会话的最后活跃时间。
func (st *SessionTracker) Touch(sessionID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if s, ok := st.sessions[sessionID]; ok {
		s.LastActive = time.Now()
	}
}

// Get 查询会话信息。
func (st *SessionTracker) Get(sessionID string) (*SessionInfo, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	s, ok := st.sessions[sessionID]
	return s, ok
}

// List 返回所有活跃会话。
func (st *SessionTracker) List() []*SessionInfo {
	st.mu.RLock()
	defer st.mu.RUnlock()

	result := make([]*SessionInfo, 0, len(st.sessions))
	for _, s := range st.sessions {
		result = append(result, s)
	}
	return result
}

// ListByDevice 返回指定设备的所有活跃会话。
func (st *SessionTracker) ListByDevice(deviceID string) []*SessionInfo {
	st.mu.RLock()
	defer st.mu.RUnlock()

	var result []*SessionInfo
	for _, s := range st.sessions {
		if s.DeviceID == deviceID {
			result = append(result, s)
		}
	}
	return result
}

// Count 返回活跃会话总数。
func (st *SessionTracker) Count() int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return len(st.sessions)
}

// CountByDevice 返回指定设备的活跃会话数。
func (st *SessionTracker) CountByDevice(deviceID string) int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	count := 0
	for _, s := range st.sessions {
		if s.DeviceID == deviceID {
			count++
		}
	}
	return count
}

// RemoveByDevice 移除指定设备的所有会话。
func (st *SessionTracker) RemoveByDevice(deviceID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for sid, s := range st.sessions {
		if s.DeviceID == deviceID {
			delete(st.sessions, sid)
		}
	}
}
