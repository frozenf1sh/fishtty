package server

import (
	"testing"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
)

func TestRelay_RegisterAgent(t *testing.T) {
	reg := NewDeviceRegistry()
	reg.Register("dev-001", "token", "1.0", "pc", "linux")
	relay := NewRelay(reg)

	sendCh := make(chan *fishttyv1.TunnelMessage, 8)
	relay.RegisterAgent("dev-001", sendCh)

	if relay.ConnectedAgentCount() != 1 {
		t.Errorf("期望 1 个 Agent，实际=%d", relay.ConnectedAgentCount())
	}

	relay.UnregisterAgent("dev-001")
	if relay.ConnectedAgentCount() != 0 {
		t.Errorf("注销后期望 0 个 Agent，实际=%d", relay.ConnectedAgentCount())
	}
}

func TestRelay_RegisterMobile(t *testing.T) {
	reg := NewDeviceRegistry()
	reg.Register("dev-001", "token", "1.0", "pc", "linux")
	relay := NewRelay(reg)

	sendCh := make(chan *fishttyv1.TunnelMessage, 8)
	relay.RegisterMobile("conn-1", "dev-001", sendCh)

	if relay.ConnectedMobileCount() != 1 {
		t.Errorf("期望 1 个 Mobile，实际=%d", relay.ConnectedMobileCount())
	}

	relay.UnregisterMobile("conn-1")
	if relay.ConnectedMobileCount() != 0 {
		t.Errorf("注销后期望 0 个 Mobile，实际=%d", relay.ConnectedMobileCount())
	}
}

func TestRelay_RouteFromAgent_DataChunk(t *testing.T) {
	reg := NewDeviceRegistry()
	reg.Register("dev-001", "token", "1.0", "pc", "linux")
	relay := NewRelay(reg)

	// 注册 Agent
	agentCh := make(chan *fishttyv1.TunnelMessage, 8)
	relay.RegisterAgent("dev-001", agentCh)

	// 注册 Mobile
	mobileCh := make(chan *fishttyv1.TunnelMessage, 16)
	relay.RegisterMobile("conn-1", "dev-001", mobileCh)

	// Agent 发送 DataChunk
	msg := &fishttyv1.TunnelMessage{
		SessionId: "session-1",
		Payload: &fishttyv1.TunnelMessage_DataChunk{
			DataChunk: &fishttyv1.DataChunk{
				Seq:  1,
				Data: []byte("hello"),
			},
		},
	}
	relay.RouteFromAgent("dev-001", msg)

	// Mobile 应收到该消息
	select {
	case received := <-mobileCh:
		if received.SessionId != "session-1" {
			t.Errorf("期望 session-1，实际=%s", received.SessionId)
		}
		chunk := received.GetDataChunk()
		if chunk == nil || string(chunk.Data) != "hello" {
			t.Errorf("期望 DataChunk=hello，实际=%v", chunk)
		}
	default:
		t.Error("Mobile 应收到 Agent 的数据")
	}
}

func TestRelay_RouteFromMobile_ToAgent(t *testing.T) {
	reg := NewDeviceRegistry()
	reg.Register("dev-001", "token", "1.0", "pc", "linux")
	relay := NewRelay(reg)

	// 注册 Agent
	agentCh := make(chan *fishttyv1.TunnelMessage, 8)
	relay.RegisterAgent("dev-001", agentCh)

	// 注册 Mobile
	mobileCh := make(chan *fishttyv1.TunnelMessage, 8)
	relay.RegisterMobile("conn-1", "dev-001", mobileCh)

	// 先通过 SessionCreated 建立 session→device 映射
	relay.RouteFromAgent("dev-001", &fishttyv1.TunnelMessage{
		SessionId: "session-1",
		Payload: &fishttyv1.TunnelMessage_SessionCreated{
			SessionCreated: &fishttyv1.SessionCreated{
				SessionId: "session-1",
				Status:    fishttyv1.SessionStatus_SESSION_STATUS_OK,
			},
		},
	})

	// Mobile 发送 DataChunk（stdin 输入）
	msg := &fishttyv1.TunnelMessage{
		SessionId: "session-1",
		Payload: &fishttyv1.TunnelMessage_DataChunk{
			DataChunk: &fishttyv1.DataChunk{
				Data: []byte("ls\n"),
			},
		},
	}
	relay.RouteFromMobile("conn-1", msg)

	// Agent 应收到该消息
	select {
	case received := <-agentCh:
		if received.SessionId != "session-1" {
			t.Errorf("期望 session-1，实际=%s", received.SessionId)
		}
		chunk := received.GetDataChunk()
		if chunk == nil || string(chunk.Data) != "ls\n" {
			t.Errorf("期望 DataChunk=ls\\n，实际=%v", chunk)
		}
	default:
		t.Error("Agent 应收到 Mobile 的数据")
	}
}

func TestRelay_SessionInitRouting(t *testing.T) {
	reg := NewDeviceRegistry()
	reg.Register("dev-001", "token", "1.0", "pc", "linux")
	relay := NewRelay(reg)

	// 注册 Agent
	agentCh := make(chan *fishttyv1.TunnelMessage, 8)
	relay.RegisterAgent("dev-001", agentCh)

	// 注册两个 Mobile
	mobileCh1 := make(chan *fishttyv1.TunnelMessage, 16)
	mobileCh2 := make(chan *fishttyv1.TunnelMessage, 16)
	relay.RegisterMobile("conn-1", "dev-001", mobileCh1)
	relay.RegisterMobile("conn-2", "dev-001", mobileCh2)

	// conn-1 发起 SessionInit
	relay.RouteFromMobile("conn-1", &fishttyv1.TunnelMessage{
		SessionId: "session-99",
		Payload: &fishttyv1.TunnelMessage_SessionInit{
			SessionInit: &fishttyv1.SessionInit{
				SessionId: "session-99",
				Cols:      80,
				Rows:      24,
			},
		},
	})

	// Agent 应收到 SessionInit
	select {
	case <-agentCh:
		// ok
	default:
		t.Error("Agent 应收到 SessionInit")
	}

	// Agent 回复 SessionCreated → 应只发给 conn-1（发起者）
	relay.RouteFromAgent("dev-001", &fishttyv1.TunnelMessage{
		SessionId: "session-99",
		Payload: &fishttyv1.TunnelMessage_SessionCreated{
			SessionCreated: &fishttyv1.SessionCreated{
				SessionId: "session-99",
				Status:    fishttyv1.SessionStatus_SESSION_STATUS_OK,
			},
		},
	})

	// conn-1 应收到 SessionCreated
	select {
	case received := <-mobileCh1:
		if _, ok := received.Payload.(*fishttyv1.TunnelMessage_SessionCreated); !ok {
			t.Errorf("conn-1 应收到 SessionCreated，实际=%T", received.Payload)
		}
	default:
		t.Error("conn-1 应收到 SessionCreated")
	}

	// conn-2 不应收到 SessionCreated（未经请求）
	select {
	case <-mobileCh2:
		t.Error("conn-2 不应收到 SessionCreated")
	default:
		// ok
	}
}

func TestRelay_UnregisterAgentCleansUp(t *testing.T) {
	reg := NewDeviceRegistry()
	reg.Register("dev-001", "token", "1.0", "pc", "linux")
	relay := NewRelay(reg)

	agentCh := make(chan *fishttyv1.TunnelMessage, 8)
	relay.RegisterAgent("dev-001", agentCh)

	// 创建 session 映射
	relay.RouteFromAgent("dev-001", &fishttyv1.TunnelMessage{
		SessionId: "session-1",
		Payload: &fishttyv1.TunnelMessage_SessionCreated{
			SessionCreated: &fishttyv1.SessionCreated{
				SessionId: "session-1",
				Status:    fishttyv1.SessionStatus_SESSION_STATUS_OK,
			},
		},
	})

	if relay.ActiveSessionCount() != 1 {
		t.Errorf("期望 1 个活跃 session，实际=%d", relay.ActiveSessionCount())
	}

	// 注销 Agent 应清理 session
	relay.UnregisterAgent("dev-001")
	if relay.ActiveSessionCount() != 0 {
		t.Errorf("注销后期望 0 个活跃 session，实际=%d", relay.ActiveSessionCount())
	}
	if relay.ConnectedAgentCount() != 0 {
		t.Error("注销后 Agent 数应为 0")
	}
}
