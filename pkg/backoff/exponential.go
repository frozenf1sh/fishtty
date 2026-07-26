// Package backoff 提供指数退避重连策略。
// 初始延迟 1s，每次翻倍，上限 60s。
// 稳定连接 ≥ 30s 后自动重置。
package backoff

import (
	"sync"
	"time"
)

// Exponential 是线程安全的指数退避计时器。
type Exponential struct {
	mu          sync.Mutex
	minDelay    time.Duration
	maxDelay    time.Duration
	current     time.Duration
	resetAfter  time.Duration
	lastResetAt time.Time
}

// NewExponential 创建一个指数退避实例。
func NewExponential(min, max, resetAfter time.Duration) *Exponential {
	return &Exponential{
		minDelay:   min,
		maxDelay:   max,
		current:    min,
		resetAfter: resetAfter,
	}
}

// DefaultExponential 使用默认参数：1s → 60s，重置阈值 30s。
func DefaultExponential() *Exponential {
	return NewExponential(time.Second, 60*time.Second, 30*time.Second)
}

// Next 返回当前延时并翻倍（不超过 maxDelay）。
func (e *Exponential) Next() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.current
	e.current *= 2
	if e.current > e.maxDelay {
		e.current = e.maxDelay
	}
	return d
}

// ResetIfStable 如果距上次重置超过 resetAfter，将退避重置为初始值。
func (e *Exponential) ResetIfStable() {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if now.Sub(e.lastResetAt) >= e.resetAfter {
		e.current = e.minDelay
		e.lastResetAt = now
	}
}

// Reset 强制重置退避到初始值。
func (e *Exponential) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.current = e.minDelay
	e.lastResetAt = time.Now()
}
