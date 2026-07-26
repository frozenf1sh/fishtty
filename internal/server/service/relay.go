package service

import (
	"fmt"
	"log/slog"
	"sync"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	"github.com/frozenf1sh/fishpts/internal/domain"
)

// 编译时验证
var _ domain.RelayRouter = (*Relay)(nil)

// channelSender 实现 domain.MessageSender，通过 channel 发消息。
type channelSender struct {
	ch     chan<- *fishttyv1.TunnelMessage
	logger *slog.Logger
}

func (s *channelSender) SendMessage(msg *fishttyv1.TunnelMessage) error {
	select {
	case s.ch <- msg: return nil
	default:
		s.logger.Warn("⚠️ 发送通道已满，消息被丢弃",
			"session_id", msg.SessionId, "payload_type", fmt.Sprintf("%T", msg.Payload))
		return fmt.Errorf("channel full")
	}
}

// Relay 实现 domain.RelayRouter，维护 Agent↔Mobile 的双向路由映射。
type Relay struct {
	mu            sync.RWMutex
	agents        map[string]chan<- *fishttyv1.TunnelMessage
	mobiles       map[string]channelSender
	mobileDevice  map[string]string
	sessionOwners map[string]string
	pendingInits  map[string]string
	logger        *slog.Logger
}

// NewRelay 创建中继实例。
func NewRelay() *Relay {
	return &Relay{
		agents:        make(map[string]chan<- *fishttyv1.TunnelMessage),
		mobiles:       make(map[string]channelSender),
		mobileDevice:  make(map[string]string),
		sessionOwners: make(map[string]string),
		pendingInits:  make(map[string]string),
		logger:        slog.Default().With("component", "relay"),
	}
}

// ── domain.RelayRouter 实现 ──

func (r *Relay) RegisterAgent(deviceID string, sender domain.MessageSender) {
	r.mu.Lock(); defer r.mu.Unlock()
	ch := make(chan *fishttyv1.TunnelMessage, 256)
	r.agents[deviceID] = ch
	go r.agentDrainLoop(sender, ch, deviceID)
	r.logger.Info("Agent 已注册到中继", "device_id", deviceID)
}

func (r *Relay) UnregisterAgent(deviceID string) {
	r.mu.Lock(); defer r.mu.Unlock()
	delete(r.agents, deviceID)
	for sid, did := range r.sessionOwners {
		if did == deviceID { delete(r.sessionOwners, sid); delete(r.pendingInits, sid) }
	}
	r.logger.Info("Agent 已从中继注销", "device_id", deviceID)
}

func (r *Relay) RegisterMobile(connID, deviceID string, sender domain.MessageSender) {
	r.mu.Lock(); defer r.mu.Unlock()
	ch := make(chan *fishttyv1.TunnelMessage, 256)
	r.mobiles[connID] = channelSender{ch: ch, logger: r.logger}
	r.mobileDevice[connID] = deviceID
	go r.mobileDrainLoop(sender, ch, connID)
	r.logger.Info("Mobile 已注册到中继", "conn_id", connID, "device_id", deviceID)
}

func (r *Relay) UnregisterMobile(connID string) {
	r.mu.Lock(); defer r.mu.Unlock()
	delete(r.mobiles, connID); delete(r.mobileDevice, connID)
	r.logger.Info("Mobile 已从中继注销", "conn_id", connID)
}

func (r *Relay) RouteFromAgent(deviceID string, msg *fishttyv1.TunnelMessage) {
	sid := msg.SessionId
	r.mu.RLock()
	if _, ok := msg.Payload.(*fishttyv1.TunnelMessage_SessionCreated); ok {
		r.mu.RUnlock(); r.mu.Lock()
		r.sessionOwners[sid] = deviceID
		if connID, ok := r.pendingInits[sid]; ok {
			delete(r.pendingInits, sid); r.mu.Unlock(); r.mu.RLock()
			if s, ok := r.mobiles[connID]; ok { s.SendMessage(msg) }
			r.mu.RUnlock(); return
		}
		r.mu.Unlock(); r.mu.RLock()
	}
	if _, ok := msg.Payload.(*fishttyv1.TunnelMessage_DataChunk); ok && sid != "" {
		r.sessionOwners[sid] = deviceID
	}
	connIDs := r.mobileConnsForDevice(deviceID)
	r.mu.RUnlock()
	for _, cid := range connIDs {
		r.mu.RLock(); s, ok := r.mobiles[cid]; r.mu.RUnlock()
		if ok { s.SendMessage(msg) }
	}
}

func (r *Relay) RouteFromMobile(connID string, msg *fishttyv1.TunnelMessage) {
	sid := msg.SessionId
	if _, ok := msg.Payload.(*fishttyv1.TunnelMessage_SessionInit); ok && sid != "" {
		r.mu.Lock(); r.pendingInits[sid] = connID; r.mu.Unlock()
	}
	r.mu.RLock()
	did := r.resolveDevice(connID, sid)
	ch, ok := r.agents[did]
	r.mu.RUnlock()
	if ok && did != "" {
		select {
		case ch <- msg:
		default:
			r.logger.Warn("⚠️ Agent 通道已满，消息被丢弃", "sid", sid, "did", did)
		}
	} else {
		r.logger.Warn("无法路由消息到 Agent", "sid", sid, "did", did, "agent_exists", ok)
	}
}

func (r *Relay) AgentCount() int     { r.mu.RLock(); defer r.mu.RUnlock(); return len(r.agents) }
func (r *Relay) MobileCount() int    { r.mu.RLock(); defer r.mu.RUnlock(); return len(r.mobiles) }
func (r *Relay) SessionCount() int   { r.mu.RLock(); defer r.mu.RUnlock(); return len(r.sessionOwners) }

// ── 内部 ──

func (r *Relay) resolveDevice(connID, sid string) string {
	if sid != "" { if did, ok := r.sessionOwners[sid]; ok { return did } }
	if did, ok := r.mobileDevice[connID]; ok { return did }
	return ""
}
func (r *Relay) mobileConnsForDevice(did string) []string {
	var out []string
	for cid, d := range r.mobileDevice { if d == did { out = append(out, cid) } }
	return out
}

// agentDrainLoop 从 channel 读取消息并通过 MessageSender 发送。
func (r *Relay) agentDrainLoop(sender domain.MessageSender, ch <-chan *fishttyv1.TunnelMessage, deviceID string) {
	for msg := range ch {
		if err := sender.SendMessage(msg); err != nil {
			r.logger.Warn("发送到 Agent 失败", "device_id", deviceID, "error", err)
			return
		}
	}
}

func (r *Relay) mobileDrainLoop(sender domain.MessageSender, ch <-chan *fishttyv1.TunnelMessage, connID string) {
	for msg := range ch {
		if err := sender.SendMessage(msg); err != nil {
			r.logger.Warn("发送到 Mobile 失败", "conn_id", connID, "error", err)
			return
		}
	}
}
