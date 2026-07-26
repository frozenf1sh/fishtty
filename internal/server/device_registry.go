// Package server 提供 fishtty-server 的核心实现。
// device_registry.go — 设备注册表，维护所有已知设备及其在线状态。
package server

import (
	"fmt"
	"sync"
	"time"
)

// DeviceStatus 表示设备的在线状态。
type DeviceStatus int

const (
	DeviceOffline DeviceStatus = iota
	DeviceOnline
)

func (s DeviceStatus) String() string {
	switch s {
	case DeviceOffline:
		return "offline"
	case DeviceOnline:
		return "online"
	default:
		return "unknown"
	}
}

// Device 表示一个注册的设备。
type Device struct {
	DeviceID     string       // 设备唯一标识
	Token        string       // 预共享认证令牌
	AgentVersion string       // Agent 版本号
	Hostname     string       // 主机名
	Platform     string       // 操作系统与架构
	Status       DeviceStatus // 在线/离线
	LastSeen     time.Time    // 最后一次心跳时间
	TunnelID     string       // 当前隧道 ID（在线时有值）
	RegisteredAt time.Time    // 设备注册时间
}

// DeviceRegistry 是设备的线程安全内存存储。
// v1 纯内存实现；v2 可迁移到 SQLite。
type DeviceRegistry struct {
	mu      sync.RWMutex
	devices map[string]*Device // deviceID → Device
}

// NewDeviceRegistry 创建新的设备注册表。
func NewDeviceRegistry() *DeviceRegistry {
	return &DeviceRegistry{
		devices: make(map[string]*Device),
	}
}

// Register 注册新设备或更新已有设备的元数据。
// 如果设备已存在，更新 AgentVersion、Hostname、Platform。
func (r *DeviceRegistry) Register(deviceID, token, agentVer, hostname, platform string) (*Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if deviceID == "" {
		return nil, fmt.Errorf("device_id 不能为空")
	}
	if token == "" {
		return nil, fmt.Errorf("token 不能为空")
	}

	d, exists := r.devices[deviceID]
	if exists {
		// 更新已有设备
		d.AgentVersion = agentVer
		d.Hostname = hostname
		d.Platform = platform
		return d, nil
	}

	// 新设备
	d = &Device{
		DeviceID:     deviceID,
		Token:        token,
		AgentVersion: agentVer,
		Hostname:     hostname,
		Platform:     platform,
		Status:       DeviceOffline,
		RegisteredAt: time.Now(),
	}
	r.devices[deviceID] = d
	return d, nil
}

// Authenticate 验证设备的 deviceID 和 token 是否匹配。
// 返回匹配的 Device，或认证失败的 error。
func (r *DeviceRegistry) Authenticate(deviceID, token string) (*Device, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	d, ok := r.devices[deviceID]
	if !ok {
		return nil, fmt.Errorf("设备 %s 未注册", deviceID)
	}
	if d.Token != token {
		return nil, fmt.Errorf("设备 %s 认证令牌不匹配", deviceID)
	}
	return d, nil
}

// SetOnline 将设备标记为在线并记录 tunnelID。
func (r *DeviceRegistry) SetOnline(deviceID, tunnelID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	d, ok := r.devices[deviceID]
	if !ok {
		return fmt.Errorf("设备 %s 未注册", deviceID)
	}
	d.Status = DeviceOnline
	d.LastSeen = time.Now()
	d.TunnelID = tunnelID
	return nil
}

// SetOffline 将设备标记为离线。
func (r *DeviceRegistry) SetOffline(deviceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if d, ok := r.devices[deviceID]; ok {
		d.Status = DeviceOffline
		d.TunnelID = ""
	}
}

// UpdateHeartbeat 更新设备的心跳时间。
func (r *DeviceRegistry) UpdateHeartbeat(deviceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if d, ok := r.devices[deviceID]; ok {
		d.LastSeen = time.Now()
	}
}

// Get 根据 deviceID 查找设备。
func (r *DeviceRegistry) Get(deviceID string) (*Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.devices[deviceID]
	return d, ok
}

// List 返回所有已注册设备的列表。
func (r *DeviceRegistry) List() []*Device {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Device, 0, len(r.devices))
	for _, d := range r.devices {
		result = append(result, d)
	}
	return result
}

// ListOnline 返回所有在线设备。
func (r *DeviceRegistry) ListOnline() []*Device {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*Device
	for _, d := range r.devices {
		if d.Status == DeviceOnline {
			result = append(result, d)
		}
	}
	return result
}

// Count 返回已注册设备总数。
func (r *DeviceRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.devices)
}

// CountOnline 返回在线设备数。
func (r *DeviceRegistry) CountOnline() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, d := range r.devices {
		if d.Status == DeviceOnline {
			count++
		}
	}
	return count
}
