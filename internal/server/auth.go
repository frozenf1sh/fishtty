package server

import (
	"fmt"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	"github.com/google/uuid"
)

// AuthenticateAgent 验证 Agent 的 AuthRequest。
// 成功时标记设备为在线、分配 tunnel_id，并返回 AuthResponse{OK}。
func AuthenticateAgent(reg *DeviceRegistry, req *fishttyv1.AuthRequest) (*fishttyv1.AuthResponse, error) {
	// 先确保设备已注册（首次连接时自动注册）
	_, err := reg.Register(
		req.DeviceId,
		req.Token,
		req.AgentVersion,
		req.Hostname,
		req.Platform,
	)
	if err != nil {
		return &fishttyv1.AuthResponse{
			Status:  fishttyv1.AuthStatus_AUTH_STATUS_UNAUTHORIZED,
			Message: fmt.Sprintf("设备注册失败: %v", err),
		}, err
	}

	// 验证 token
	_, err = reg.Authenticate(req.DeviceId, req.Token)
	if err != nil {
		return &fishttyv1.AuthResponse{
			Status:  fishttyv1.AuthStatus_AUTH_STATUS_UNAUTHORIZED,
			Message: err.Error(),
		}, err
	}

	// 分配 tunnel_id 并标记在线
	tunnelID := uuid.New().String()
	if err := reg.SetOnline(req.DeviceId, tunnelID); err != nil {
		return &fishttyv1.AuthResponse{
			Status:  fishttyv1.AuthStatus_AUTH_STATUS_UNAUTHORIZED,
			Message: err.Error(),
		}, err
	}

	return &fishttyv1.AuthResponse{
		Status:   fishttyv1.AuthStatus_AUTH_STATUS_OK,
		Message:  "认证成功",
		TunnelId: tunnelID,
	}, nil
}

// AuthenticateMobile 验证移动端 WebSocket 连接。
// v1 实现：通过 URL query param 传递 device_id，校验设备是否已注册。
func AuthenticateMobile(reg *DeviceRegistry, deviceID string) (*Device, error) {
	d, ok := reg.Get(deviceID)
	if !ok {
		return nil, fmt.Errorf("设备 %s 未注册", deviceID)
	}
	return d, nil
}
