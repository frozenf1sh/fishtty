package server

import (
	"fmt"
	"log/slog"
	"sync"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
)

// ── 中继核心 ──
//
// Relay 是 Server 的核心消息路由层。
// 它维护 Agent 连接与 Mobile WebSocket 连接的映射关系，
// 并以 TunnelMessage.session_id 作为唯一路由 Key 进行双向转发。
//
// 路由规则：
//   - Agent→Mobile：根据 session_id 查 sessionOwners → 找到 deviceID →
//     转发给连接到此 device 的所有 Mobile。
//   - Mobile→Agent：根据 session_id 查 sessionOwners → 找到 deviceID →
//     转发给此 device 的 Agent。

// Relay 是线程安全的消息中继器。
type Relay struct {
	mu sync.RWMutex

	devices *DeviceRegistry

	// agentChannels: deviceID → 发往 Agent 的通道
	agentChannels map[string]chan<- *fishttyv1.TunnelMessage

	// mobileChannels: connID → 发往 Mobile 的通道
	mobileChannels map[string]chan<- *fishttyv1.TunnelMessage

	// mobileDevice: connID → 此 Mobile 连接的 deviceID
	mobileDevice map[string]string

	// sessionOwners: sessionID → deviceID（哪个 Agent 拥有此 session）
	sessionOwners map[string]string

	// pendingInits: sessionID → connID（哪个 Mobile 发起了 SessionInit）
	// 用于将 SessionCreated 路由回正确的 Mobile 连接
	pendingInits map[string]string

	logger *slog.Logger
}

// NewRelay 创建一个新的 Relay 实例。
func NewRelay(devices *DeviceRegistry) *Relay {
	return &Relay{
		devices:        devices,
		agentChannels:  make(map[string]chan<- *fishttyv1.TunnelMessage),
		mobileChannels: make(map[string]chan<- *fishttyv1.TunnelMessage),
		mobileDevice:   make(map[string]string),
		sessionOwners:  make(map[string]string),
		pendingInits:   make(map[string]string),
		logger:         slog.Default().With("component", "relay"),
	}
}

// ── Agent 管理 ──

// RegisterAgent 注册一个 Agent 连接。
// sendCh 是发往此 Agent 的通道（由 TunnelHandler 的 sendLoop 消费）。
func (r *Relay) RegisterAgent(deviceID string, sendCh chan<- *fishttyv1.TunnelMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agentChannels[deviceID] = sendCh
	r.logger.Info("Agent 已注册到中继", "device_id", deviceID)
}

// UnregisterAgent 注销一个 Agent 连接。
func (r *Relay) UnregisterAgent(deviceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.agentChannels, deviceID)
	r.devices.SetOffline(deviceID)

	// 清理此设备的所有 session
	for sid, did := range r.sessionOwners {
		if did == deviceID {
			delete(r.sessionOwners, sid)
			delete(r.pendingInits, sid)
		}
	}

	r.logger.Info("Agent 已从中继注销", "device_id", deviceID)
}

// ── Mobile 管理 ──

// RegisterMobile 注册一个 Mobile WebSocket 连接。
// sendCh 是发往此 Mobile 的通道（由 WS handler 的 writeLoop 消费）。
func (r *Relay) RegisterMobile(connID, deviceID string, sendCh chan<- *fishttyv1.TunnelMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mobileChannels[connID] = sendCh
	r.mobileDevice[connID] = deviceID
	r.logger.Info("Mobile 已注册到中继", "conn_id", connID, "device_id", deviceID)
}

// UnregisterMobile 注销一个 Mobile WebSocket 连接。
func (r *Relay) UnregisterMobile(connID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.mobileChannels, connID)
	delete(r.mobileDevice, connID)
	r.logger.Info("Mobile 已从中继注销", "conn_id", connID)
}

// ── 消息路由 ──

// RouteFromAgent 处理从 Agent 收到的消息，转发给对应的 Mobile。
func (r *Relay) RouteFromAgent(deviceID string, msg *fishttyv1.TunnelMessage) {
	sid := msg.SessionId

	r.mu.RLock()
	// 跟踪 session 归属（收到 SessionCreated 时记录）
	if _, isCreated := msg.Payload.(*fishttyv1.TunnelMessage_SessionCreated); isCreated {
		// 需要在持有锁的情况下更新
		r.mu.RUnlock()
		r.mu.Lock()
		r.sessionOwners[sid] = deviceID
		// 将 SessionCreated 路由给发起 SessionInit 的 Mobile
		if connID, ok := r.pendingInits[sid]; ok {
			delete(r.pendingInits, sid)
			r.mu.Unlock()
			r.mu.RLock()
			if ch, ok := r.mobileChannels[connID]; ok {
				r.sendToChannel(ch, msg)
			}
			r.mu.RUnlock()
			return
		}
		r.mu.Unlock()
		r.mu.RLock()
	}

	// 记录 session 归属
	if _, isData := msg.Payload.(*fishttyv1.TunnelMessage_DataChunk); isData && sid != "" {
		r.sessionOwners[sid] = deviceID
	}

	// 获取此设备的所有 Mobile 连接
	connIDs := r.mobileConnsForDeviceLocked(deviceID)
	r.mu.RUnlock()

	// 转发给所有相关的 Mobile 连接
	for _, connID := range connIDs {
		r.mu.RLock()
		ch, ok := r.mobileChannels[connID]
		r.mu.RUnlock()
		if ok {
			r.sendToChannel(ch, msg)
		}
	}
}

// RouteFromMobile 处理从 Mobile 收到的消息，转发给对应的 Agent。
func (r *Relay) RouteFromMobile(connID string, msg *fishttyv1.TunnelMessage) {
	sid := msg.SessionId

	// 跟踪 SessionInit：记录哪个 Mobile 发起了创建
	if _, isInit := msg.Payload.(*fishttyv1.TunnelMessage_SessionInit); isInit && sid != "" {
		r.mu.Lock()
		r.pendingInits[sid] = connID
		r.mu.Unlock()
	}

	// 确定目标 deviceID
	r.mu.RLock()
	deviceID := r.resolveDeviceLocked(connID, sid)
	agentCh, ok := r.agentChannels[deviceID]
	r.mu.RUnlock()

	if ok && deviceID != "" {
		r.sendToChannel(agentCh, msg)
	} else {
		r.logger.Warn("无法路由消息到 Agent：无在线设备或通道",
			"session_id", sid,
			"device_id", deviceID,
			"agent_exists", ok,
		)
	}
}

// resolveDeviceLocked 确定消息应路由到哪个 deviceID。
// 调用者必须持有 r.mu.RLocker。
func (r *Relay) resolveDeviceLocked(connID, sessionID string) string {
	// 优先：根据 sessionID 找到归属 device
	if sessionID != "" {
		if did, ok := r.sessionOwners[sessionID]; ok {
			return did
		}
	}
	// 其次：Mobile 连接的 deviceID
	if did, ok := r.mobileDevice[connID]; ok {
		return did
	}
	return ""
}

// mobileConnsForDeviceLocked 返回连接到此设备的所有 Mobile connID。
// 调用者必须持有 r.mu.RLocker。
func (r *Relay) mobileConnsForDeviceLocked(deviceID string) []string {
	var result []string
	for connID, did := range r.mobileDevice {
		if did == deviceID {
			result = append(result, connID)
		}
	}
	return result
}

// ── 内部工具 ──

// sendToChannel 非阻塞地向通道发送消息。
// 如果通道已满，丢弃消息并记录告警（表示消费端卡住或已退出）。
func (r *Relay) sendToChannel(ch chan<- *fishttyv1.TunnelMessage, msg *fishttyv1.TunnelMessage) {
	select {
	case ch <- msg:
	default:
		r.logger.Warn("⚠️ 发送通道已满，消息被丢弃（sendLoop 可能已退出！）",
			"session_id", msg.SessionId,
			"payload_type", fmt.Sprintf("%T", msg.Payload),
			"channel_cap", 256,
		)
	}
}

// ── 查询方法 ──

// ConnectedAgentCount 返回当前连接的 Agent 数量。
func (r *Relay) ConnectedAgentCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agentChannels)
}

// ConnectedMobileCount 返回当前连接的 Mobile 数量。
func (r *Relay) ConnectedMobileCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.mobileChannels)
}

// ActiveSessionCount 返回当前活跃的 session 数量。
func (r *Relay) ActiveSessionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessionOwners)
}
