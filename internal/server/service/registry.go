// Package service 提供 Server 端应用服务——设备注册与消息中继。
package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/frozenf1sh/fishpts/internal/domain"
)

// 编译时验证
var _ domain.DeviceStore = (*DeviceRegistry)(nil)

// DeviceRegistry 实现 domain.DeviceStore，纯内存设备存储。
type DeviceRegistry struct {
	mu      sync.RWMutex
	devices map[string]*deviceRecord
}

type deviceRecord struct {
	deviceID, token, agentVer, hostname, platform string
	online                                         bool
	tunnelID                                       string
	lastSeen, registeredAt                         time.Time
}

// NewDeviceRegistry 创建设备注册表。
func NewDeviceRegistry() *DeviceRegistry {
	return &DeviceRegistry{devices: make(map[string]*deviceRecord)}
}

func (r *DeviceRegistry) Register(deviceID, token, agentVer, hostname, platform string) (*domain.Device, error) {
	r.mu.Lock(); defer r.mu.Unlock()
	if deviceID == "" { return nil, fmt.Errorf("device_id 不能为空") }
	if token == "" { return nil, fmt.Errorf("token 不能为空") }

	if d, ok := r.devices[deviceID]; ok {
		// 设备已存在：校验 token 一致性，不一致则拒绝（防止静默忽略 token 变更）
		if d.token != token {
			return nil, fmt.Errorf("设备 %s token 不匹配，请检查配置文件", deviceID)
		}
		d.agentVer, d.hostname, d.platform = agentVer, hostname, platform
		return toDomain(d), nil
	}
	d := &deviceRecord{deviceID: deviceID, token: token, agentVer: agentVer, hostname: hostname, platform: platform, registeredAt: time.Now()}
	r.devices[deviceID] = d
	return toDomain(d), nil
}

func (r *DeviceRegistry) Authenticate(deviceID, token string) (*domain.Device, error) {
	r.mu.RLock(); defer r.mu.RUnlock()
	d, ok := r.devices[deviceID]
	if !ok { return nil, fmt.Errorf("设备 %s 未注册", deviceID) }
	if d.token != token { return nil, fmt.Errorf("设备 %s 认证令牌不匹配", deviceID) }
	return toDomain(d), nil
}

func (r *DeviceRegistry) SetOnline(deviceID, tunnelID string) error {
	r.mu.Lock(); defer r.mu.Unlock()
	d, ok := r.devices[deviceID]
	if !ok { return fmt.Errorf("设备 %s 未注册", deviceID) }
	d.online, d.lastSeen, d.tunnelID = true, time.Now(), tunnelID
	return nil
}

func (r *DeviceRegistry) SetOffline(deviceID string) {
	r.mu.Lock(); defer r.mu.Unlock()
	if d, ok := r.devices[deviceID]; ok { d.online, d.tunnelID = false, "" }
}

func (r *DeviceRegistry) UpdateHeartbeat(deviceID string) {
	r.mu.Lock(); defer r.mu.Unlock()
	if d, ok := r.devices[deviceID]; ok { d.lastSeen = time.Now() }
}

func (r *DeviceRegistry) Get(deviceID string) (*domain.Device, bool) {
	r.mu.RLock(); defer r.mu.RUnlock()
	d, ok := r.devices[deviceID]
	if !ok { return nil, false }
	return toDomain(d), true
}

func (r *DeviceRegistry) List() []*domain.Device {
	r.mu.RLock(); defer r.mu.RUnlock()
	var out []*domain.Device
	for _, d := range r.devices { out = append(out, toDomain(d)) }
	return out
}

func (r *DeviceRegistry) ListOnline() []*domain.Device {
	r.mu.RLock(); defer r.mu.RUnlock()
	var out []*domain.Device
	for _, d := range r.devices { if d.online { out = append(out, toDomain(d)) } }
	return out
}

func (r *DeviceRegistry) Count() int    { r.mu.RLock(); defer r.mu.RUnlock(); return len(r.devices) }
func (r *DeviceRegistry) CountOnline() int {
	r.mu.RLock(); defer r.mu.RUnlock(); n := 0
	for _, d := range r.devices { if d.online { n++ } }
	return n
}

func toDomain(d *deviceRecord) *domain.Device {
	return &domain.Device{
		DeviceID: d.deviceID, Token: d.token, AgentVersion: d.agentVer,
		Hostname: d.hostname, Platform: d.platform, Online: d.online,
		LastSeen: d.lastSeen.UnixMilli(), TunnelID: d.tunnelID, RegisteredAt: d.registeredAt.UnixMilli(),
	}
}
