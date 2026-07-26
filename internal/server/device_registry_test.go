package server

import (
	"testing"
)

func TestDeviceRegistry_Register(t *testing.T) {
	reg := NewDeviceRegistry()

	// 注册新设备
	d, err := reg.Register("dev-001", "token-abc", "1.0.0", "my-pc", "linux/amd64")
	if err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	if d.DeviceID != "dev-001" {
		t.Errorf("期望 deviceID=dev-001，实际=%s", d.DeviceID)
	}
	if d.Status != DeviceOffline {
		t.Errorf("新设备应为离线状态，实际=%s", d.Status)
	}

	// 重复注册应更新元数据
	d2, err := reg.Register("dev-001", "token-abc", "2.0.0", "my-pc-v2", "linux/arm64")
	if err != nil {
		t.Fatalf("重复注册失败: %v", err)
	}
	if d2.AgentVersion != "2.0.0" {
		t.Errorf("版本号应更新为 2.0.0，实际=%s", d2.AgentVersion)
	}
	if d2.Hostname != "my-pc-v2" {
		t.Errorf("主机名应更新为 my-pc-v2，实际=%s", d2.Hostname)
	}
}

func TestDeviceRegistry_Authenticate(t *testing.T) {
	reg := NewDeviceRegistry()
	reg.Register("dev-001", "token-secret", "1.0.0", "pc", "linux/amd64")

	// 正确 token
	_, err := reg.Authenticate("dev-001", "token-secret")
	if err != nil {
		t.Errorf("正确的 token 应认证成功: %v", err)
	}

	// 错误 token
	_, err = reg.Authenticate("dev-001", "wrong-token")
	if err == nil {
		t.Error("错误 token 应认证失败")
	}

	// 不存在的设备
	_, err = reg.Authenticate("dev-999", "token-secret")
	if err == nil {
		t.Error("不存在的设备应认证失败")
	}
}

func TestDeviceRegistry_SetOnlineOffline(t *testing.T) {
	reg := NewDeviceRegistry()
	reg.Register("dev-001", "token", "1.0.0", "pc", "linux/amd64")

	// 上线
	if err := reg.SetOnline("dev-001", "tunnel-123"); err != nil {
		t.Fatalf("SetOnline 失败: %v", err)
	}

	d, _ := reg.Get("dev-001")
	if d.Status != DeviceOnline {
		t.Errorf("设备应在线，实际=%s", d.Status)
	}
	if d.TunnelID != "tunnel-123" {
		t.Errorf("TunnelID 应为 tunnel-123，实际=%s", d.TunnelID)
	}

	// 离线
	reg.SetOffline("dev-001")
	if d.Status != DeviceOffline {
		t.Errorf("设备应离线，实际=%s", d.Status)
	}
	if d.TunnelID != "" {
		t.Errorf("TunnelID 应为空，实际=%s", d.TunnelID)
	}
}

func TestDeviceRegistry_UpdateHeartbeat(t *testing.T) {
	reg := NewDeviceRegistry()
	reg.Register("dev-001", "token", "1.0.0", "pc", "linux/amd64")
	reg.SetOnline("dev-001", "tunnel-123")

	d, _ := reg.Get("dev-001")
	oldSeen := d.LastSeen

	reg.UpdateHeartbeat("dev-001")

	d, _ = reg.Get("dev-001")
	if !d.LastSeen.After(oldSeen) {
		t.Error("心跳应更新 LastSeen 时间")
	}
}

func TestDeviceRegistry_Count(t *testing.T) {
	reg := NewDeviceRegistry()

	reg.Register("dev-001", "t1", "1.0", "a", "linux")
	reg.Register("dev-002", "t2", "1.0", "b", "linux")
	reg.Register("dev-003", "t3", "1.0", "c", "linux")

	if reg.Count() != 3 {
		t.Errorf("期望 3 个设备，实际=%d", reg.Count())
	}

	reg.SetOnline("dev-001", "t1")
	reg.SetOnline("dev-002", "t2")

	if reg.CountOnline() != 2 {
		t.Errorf("期望 2 个在线设备，实际=%d", reg.CountOnline())
	}
}

func TestDeviceRegistry_List(t *testing.T) {
	reg := NewDeviceRegistry()
	reg.Register("dev-001", "t1", "1.0", "a", "linux")
	reg.Register("dev-002", "t2", "1.0", "b", "linux")

	all := reg.List()
	if len(all) != 2 {
		t.Errorf("期望 2 个设备，实际=%d", len(all))
	}

	reg.SetOnline("dev-001", "t1")
	online := reg.ListOnline()
	if len(online) != 1 {
		t.Errorf("期望 1 个在线设备，实际=%d", len(online))
	}
	if online[0].DeviceID != "dev-001" {
		t.Errorf("在线设备应为 dev-001，实际=%s", online[0].DeviceID)
	}
}
