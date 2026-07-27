// Package domain 定义 fishtty 的核心领域模型与接口。
// 所有接口遵循 ISP（接口隔离原则），只暴露调用方需要的最小方法集。
// 具体实现在 internal/agent/adapter 和 internal/server/adapter 中。
package domain

import (
	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
)

// ── 传输层抽象 ──

// MessageSender 是发消息的抽象（Connect-RPC stream / WebSocket 连接都实现它）。
type MessageSender interface {
	SendMessage(msg *fishttyv1.TunnelMessage) error
}

// MessageReceiver 是收消息的抽象。
type MessageReceiver interface {
	ReceiveMessage() (*fishttyv1.TunnelMessage, error)
}

// StreamConn 表示一个双向消息流（Agent↔Server 隧道）。
// Connect-RPC 的 BidiStream 和 WebSocket 连接都符合此接口。
type StreamConn interface {
	MessageSender
	MessageReceiver
	CloseRequest() error
}

// ── PTY 抽象 ──

// TerminalEmulator 是 PTY 伪终端的行为抽象。
// 具体实现使用 creack/pty。
type TerminalEmulator interface {
	Read(buf []byte) (int, error)
	Write(data []byte) (int, error)
	Resize(cols, rows uint32) error
	Close() error
	Pid() int
}

// TerminalConfig PTY 创建参数。
type TerminalConfig struct {
	Command string
	Cols    uint32
	Rows    uint32
	Env     map[string]string
	WorkDir string
}

// TerminalFactory 创建 PTY 终端实例。
type TerminalFactory interface {
	Create(cfg TerminalConfig) (TerminalEmulator, error)
}

// ── 输出缓冲区 ──

// OutputBuffer 是 PTY 输出历史的环形缓冲抽象。
// 支持按序号增量重放，用于 Reattach 断连恢复。
type OutputBuffer interface {
	Append(seq uint64, data []byte) int
	ReplayFrom(lastSeq uint64) ([]*fishttyv1.DataChunk, uint64)
	OldestSeq() uint64
	NewestSeq() uint64
	Len() int
}

// ── 消息路由 ──

// MessageDispatcher 将从流收到的消息分发给对应的处理器。
type MessageDispatcher interface {
	Dispatch(msg *fishttyv1.TunnelMessage)
}

// ── Session 生命周期 ──

// SessionManager 管理 PTY 会话的生命周期（创建、查找、销毁）。
type SessionManager interface {
	Create(init *fishttyv1.SessionInit) (*fishttyv1.SessionCreated, error)
	Get(sid string) (Session, bool)
	Destroy(sid string) error
	DestroyAll()
	Count() int
}

// Session 是单个 PTY 会话的行为抽象。
type Session interface {
	ID() string
	WriteStdin(data []byte) error
	Resize(cols, rows uint32) error
	ReplayFrom(lastSeq uint64) *fishttyv1.ReattachData
	Destroy()
}

// ── 中继（Server 端） ──

// RelayRouter 是 Server 端的中继消息路由抽象。
// 维护 Agent ↔ Mobile 的双向映射，按 session_id 转发消息。
type RelayRouter interface {
	RegisterAgent(deviceID string, sender MessageSender)
	UnregisterAgent(deviceID string)
	RegisterMobile(connID, deviceID string, sender MessageSender)
	UnregisterMobile(connID string)
	RouteFromAgent(deviceID string, msg *fishttyv1.TunnelMessage)
	RouteFromMobile(connID string, msg *fishttyv1.TunnelMessage)
	CleanSession(sid string) // 清理指定 session 的 ownership 映射
	AgentCount() int
	MobileCount() int
	SessionCount() int
}

// ── 设备存储 ──

// DeviceStore 管理已注册设备的信息与在线状态。
// v1 纯内存实现；v2 可迁移到 SQLite。
type DeviceStore interface {
	Register(deviceID, token, agentVer, hostname, platform string) (*Device, error)
	Authenticate(deviceID, token string) (*Device, error)
	SetOnline(deviceID, tunnelID string) error
	SetOffline(deviceID string)
	UpdateHeartbeat(deviceID string)
	Get(deviceID string) (*Device, bool)
	List() []*Device
	ListOnline() []*Device
	Count() int
	CountOnline() int
}

// Device 领域实体。
type Device struct {
	DeviceID     string
	Token        string
	AgentVersion string
	Hostname     string
	Platform     string
	Online       bool
	LastSeen     int64 // Unix 毫秒
	TunnelID     string
	RegisteredAt int64
}
