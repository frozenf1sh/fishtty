// Package auth 提供 Server 端认证逻辑。
package auth

import (
	"fmt"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	"github.com/frozenf1sh/fishpts/internal/domain"
	"github.com/google/uuid"
)

// Agent 验证 Agent 的 AuthRequest。
// 首次连接自动注册设备，成功时分配 tunnel_id 并标记在线。
func Agent(store domain.DeviceStore, req *fishttyv1.AuthRequest) (*fishttyv1.AuthResponse, error) {
	_, err := store.Register(req.DeviceId, req.Token, req.AgentVersion, req.Hostname, req.Platform)
	if err != nil {
		return &fishttyv1.AuthResponse{Status: fishttyv1.AuthStatus_AUTH_STATUS_UNAUTHORIZED, Message: fmt.Sprintf("设备注册失败: %v", err)}, err
	}

	if _, err := store.Authenticate(req.DeviceId, req.Token); err != nil {
		return &fishttyv1.AuthResponse{Status: fishttyv1.AuthStatus_AUTH_STATUS_UNAUTHORIZED, Message: err.Error()}, err
	}

	tunnelID := uuid.New().String()
	if err := store.SetOnline(req.DeviceId, tunnelID); err != nil {
		return &fishttyv1.AuthResponse{Status: fishttyv1.AuthStatus_AUTH_STATUS_UNAUTHORIZED, Message: err.Error()}, err
	}
	return &fishttyv1.AuthResponse{Status: fishttyv1.AuthStatus_AUTH_STATUS_OK, Message: "认证成功", TunnelId: tunnelID}, nil
}

// Mobile 验证移动端 WebSocket 连接。
func Mobile(store domain.DeviceStore, deviceID string) error {
	if _, ok := store.Get(deviceID); !ok {
		return fmt.Errorf("设备 %s 未注册", deviceID)
	}
	return nil
}
