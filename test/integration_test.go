// Package test 包含 fishtty 的集成测试。
//
// 测试服务器已启用 UnencryptedHTTP2（h2c），Connect-RPC 双向流测试可直接运行。

package test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	fishttyv1connect "github.com/frozenf1sh/fishpts/gen/fishtty/v1/fishttyv1connect"
	"github.com/frozenf1sh/fishpts/internal/server"
	"github.com/gorilla/websocket"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/proto"
)

func startServer(t *testing.T) (string, *server.DeviceRegistry, *server.Relay, func()) {
	t.Helper()

	devices := server.NewDeviceRegistry()
	relay := server.NewRelay(devices)

	mux := http.NewServeMux()
	tunnelHandler := server.NewTunnelHandler(devices, relay)
	tunnelPath, tunnelHTTP := tunnelHandler.Handler()
	mux.Handle(tunnelPath, tunnelHTTP)
	mux.Handle("/ws", server.NewWSHandler(devices, relay))

	// 启用 UnencryptedHTTP2，使 Connect-RPC 双向流测试能运行
	protocols := &http.Protocols{}
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	srv := &http.Server{Addr: "127.0.0.1:0", Handler: mux, Protocols: protocols}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	go srv.Serve(ln)

	cleanup := func() { srv.Close() }
	t.Cleanup(cleanup)
	time.Sleep(50 * time.Millisecond)

	return fmt.Sprintf("http://%s", ln.Addr().String()), devices, relay, cleanup
}

// h2cHTTPClient 创建支持 HTTP/2 Cleartext 的客户端（与 agent/tunnel.go 一致）
func h2cHTTPClient() *http.Client {
	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{Transport: transport}
}

// ── Connect-RPC 双向流测试（h2c） ──

func TestIntegration_ConnectRPC_Tunnel(t *testing.T) {
	addr, devices, relay, _ := startServer(t)
	devices.Register("dev-h2c-1", "tok-secret", "1.0.0", "test-pc", "darwin/arm64")

	client := fishttyv1connect.NewFishTTYClient(h2cHTTPClient(), addr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := client.Tunnel(ctx)

	// 发送 AuthRequest
	err := stream.Send(&fishttyv1.TunnelMessage{
		Payload: &fishttyv1.TunnelMessage_AuthReq{
			AuthReq: &fishttyv1.AuthRequest{
				DeviceId: "dev-h2c-1", Token: "tok-secret",
				AgentVersion: "1.0.0", Hostname: "test-pc", Platform: "darwin/arm64",
			},
		},
	})
	if err != nil {
		t.Fatalf("发送 AuthRequest 失败: %v", err)
	}

	// 接收 AuthResponse
	msg, err := stream.Receive()
	if err != nil {
		t.Fatalf("接收 AuthResponse 失败: %v", err)
	}

	authResp := msg.GetAuthResp()
	if authResp == nil || authResp.Status != fishttyv1.AuthStatus_AUTH_STATUS_OK {
		t.Fatalf("认证失败: %v", msg.Payload)
	}
	t.Logf("✅ h2c 认证成功: tunnel_id=%s", authResp.TunnelId)

	if relay.ConnectedAgentCount() != 1 {
		t.Error("Agent 未在 Relay 中注册")
	}
}

func TestIntegration_ConnectRPC_Heartbeat(t *testing.T) {
	addr, devices, _, _ := startServer(t)
	devices.Register("dev-hb", "tok", "1.0", "pc", "linux")

	client := fishttyv1connect.NewFishTTYClient(h2cHTTPClient(), addr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := client.Tunnel(ctx)
	_ = stream.Send(&fishttyv1.TunnelMessage{
		Payload: &fishttyv1.TunnelMessage_AuthReq{
			AuthReq: &fishttyv1.AuthRequest{
				DeviceId: "dev-hb", Token: "tok",
				AgentVersion: "1.0", Hostname: "pc", Platform: "linux",
			},
		},
	})
	stream.Receive() // AuthResponse

	ts := time.Now().UnixMilli()
	_ = stream.Send(&fishttyv1.TunnelMessage{
		Payload: &fishttyv1.TunnelMessage_Heartbeat{
			Heartbeat: &fishttyv1.Heartbeat{Timestamp: ts},
		},
	})

	ackMsg, err := stream.Receive()
	if err != nil {
		t.Fatalf("接收 HeartbeatAck 失败: %v", err)
	}
	ack := ackMsg.GetHeartbeatAck()
	if ack == nil || ack.Timestamp != ts {
		t.Fatalf("HeartbeatAck 不匹配")
	}
	t.Log("✅ h2c Heartbeat 测试通过")
}

func TestIntegration_ConnectRPC_SessionInit(t *testing.T) {
	addr, devices, relay, _ := startServer(t)
	devices.Register("dev-sess", "tok", "1.0", "pc", "linux")

	client := fishttyv1connect.NewFishTTYClient(h2cHTTPClient(), addr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream := client.Tunnel(ctx)
	_ = stream.Send(&fishttyv1.TunnelMessage{
		Payload: &fishttyv1.TunnelMessage_AuthReq{
			AuthReq: &fishttyv1.AuthRequest{
				DeviceId: "dev-sess", Token: "tok",
				AgentVersion: "1.0", Hostname: "pc", Platform: "linux",
			},
		},
	})
	stream.Receive()

	sessionID := "sess-h2c-1"

	// 注册 Mobile 通道
	mobileCh := make(chan *fishttyv1.TunnelMessage, 8)
	relay.RegisterMobile("m-sess", "dev-sess", mobileCh)
	defer relay.UnregisterMobile("m-sess")

	// Mobile 发送 SessionInit → Relay 路由到 Agent
	go func() {
		relay.RouteFromMobile("m-sess", &fishttyv1.TunnelMessage{
			SessionId: sessionID,
			Payload: &fishttyv1.TunnelMessage_SessionInit{
				SessionInit: &fishttyv1.SessionInit{
					SessionId: sessionID, Cols: 80, Rows: 24,
				},
			},
		})
	}()

	// Agent 接收 SessionInit
	initMsg, err := stream.Receive()
	if err != nil {
		t.Fatalf("Agent 接收 SessionInit 失败: %v", err)
	}
	if initMsg.GetSessionInit() == nil {
		t.Fatalf("期望 SessionInit")
	}
	t.Logf("✅ Agent 通过 h2c 隧道收到 SessionInit: %s", initMsg.GetSessionInit().SessionId)

	// Agent 回复 SessionCreated
	_ = stream.Send(&fishttyv1.TunnelMessage{
		SessionId: sessionID,
		Payload: &fishttyv1.TunnelMessage_SessionCreated{
			SessionCreated: &fishttyv1.SessionCreated{
				SessionId: sessionID, Status: fishttyv1.SessionStatus_SESSION_STATUS_OK,
			},
		},
	})

	// Agent 发送 DataChunk
	_ = stream.Send(&fishttyv1.TunnelMessage{
		SessionId: sessionID,
		Payload: &fishttyv1.TunnelMessage_DataChunk{
			DataChunk: &fishttyv1.DataChunk{Seq: 1, Data: []byte("hello via h2c")},
		},
	})

	// 收集 Mobile 收到的消息（SessionCreated + DataChunk 顺序不定）
	var gotCreated, gotData bool
	timeout := time.After(3 * time.Second)
	for !gotCreated || !gotData {
		select {
		case msg := <-mobileCh:
			if msg.GetSessionCreated() != nil {
				gotCreated = true
			}
			if dc := msg.GetDataChunk(); dc != nil {
				gotData = true
				if string(dc.Data) != "hello via h2c" {
					t.Errorf("DataChunk 内容: %q", string(dc.Data))
				}
				t.Logf("✅ h2c 隧道 DataChunk: %s", string(dc.Data))
			}
		case <-timeout:
			t.Fatalf("超时: created=%v data=%v", gotCreated, gotData)
		}
	}
}

// netListen 的包装
func netListen(network, address string) (net.Listener, error) {
	return net.Listen(network, address)
}

// ── Relay 消息路由测试 ──

func TestIntegration_MessageRouting(t *testing.T) {
	_, devices, relay, _ := startServer(t)
	devices.Register("dev-routing", "tok", "1.0", "pc", "linux")

	mobileCh := make(chan *fishttyv1.TunnelMessage, 16)
	relay.RegisterMobile("mobile-1", "dev-routing", mobileCh)
	defer relay.UnregisterMobile("mobile-1")

	sessionID := "session-route-1"

	// Agent 发送 SessionCreated → Mobile 收到
	relay.RouteFromAgent("dev-routing", &fishttyv1.TunnelMessage{
		SessionId: sessionID,
		Payload: &fishttyv1.TunnelMessage_SessionCreated{
			SessionCreated: &fishttyv1.SessionCreated{
				SessionId: sessionID, Status: fishttyv1.SessionStatus_SESSION_STATUS_OK,
			},
		},
	})

	for i := 1; i <= 5; i++ {
		relay.RouteFromAgent("dev-routing", &fishttyv1.TunnelMessage{
			SessionId: sessionID,
			Payload: &fishttyv1.TunnelMessage_DataChunk{
				DataChunk: &fishttyv1.DataChunk{
					Seq: uint64(i), Data: []byte(fmt.Sprintf("chunk-%d", i)),
				},
			},
		})
	}

	count := 0
	timeout := time.After(2 * time.Second)
	for count < 5 {
		select {
		case msg := <-mobileCh:
			if msg.GetDataChunk() != nil {
				count++
			}
		case <-timeout:
			t.Fatalf("只收到 %d/5 DataChunk", count)
		}
	}
	t.Logf("Relay 路由：%d 条 DataChunk 全部送达", count)
}

// ── Reattach 增量补发测试 ──

func TestIntegration_ReattachFlow(t *testing.T) {
	_, devices, relay, _ := startServer(t)
	devices.Register("dev-rea", "tok", "1.0", "pc", "linux")

	agentCh := make(chan *fishttyv1.TunnelMessage, 8)
	relay.RegisterAgent("dev-rea", agentCh)
	defer relay.UnregisterAgent("dev-rea")

	mobileCh := make(chan *fishttyv1.TunnelMessage, 8)
	relay.RegisterMobile("mobile-rea", "dev-rea", mobileCh)

	sessionID := "session-rea-1"

	// 建立 session 映射
	relay.RouteFromAgent("dev-rea", &fishttyv1.TunnelMessage{
		SessionId: sessionID,
		Payload: &fishttyv1.TunnelMessage_SessionCreated{
			SessionCreated: &fishttyv1.SessionCreated{
				SessionId: sessionID, Status: fishttyv1.SessionStatus_SESSION_STATUS_OK,
			},
		},
	})
	// Mobile 收到 SessionCreated（drain）
	<-mobileCh

	// Mobile 发送 Reattach（last_ack_seq=5）
	relay.RouteFromMobile("mobile-rea", &fishttyv1.TunnelMessage{
		SessionId: sessionID,
		Payload: &fishttyv1.TunnelMessage_Reattach{
			Reattach: &fishttyv1.Reattach{SessionId: sessionID, LastAckSeq: 5},
		},
	})

	// Agent 应收
	select {
	case msg := <-agentCh:
		r := msg.GetReattach()
		if r == nil || r.LastAckSeq != 5 {
			t.Errorf("期望 Reattach(last_ack_seq=5)，收到 %v", msg.Payload)
		}
		t.Logf("Agent 收到 Reattach: last_ack_seq=%d", r.LastAckSeq)
	case <-time.After(2 * time.Second):
		t.Fatal("Agent 未收到 Reattach")
	}

	// Agent 回复 ReattachData
	relay.RouteFromAgent("dev-rea", &fishttyv1.TunnelMessage{
		SessionId: sessionID,
		Payload: &fishttyv1.TunnelMessage_ReattachData{
			ReattachData: &fishttyv1.ReattachData{
				SessionId: sessionID,
				StartSeq:  6,
				Chunks: []*fishttyv1.DataChunk{
					{Seq: 6, Data: []byte("chunk-6")},
					{Seq: 7, Data: []byte("chunk-7")},
				},
			},
		},
	})

	// Mobile 应收 ReattachData
	select {
	case msg := <-mobileCh:
		rd := msg.GetReattachData()
		if rd == nil || len(rd.Chunks) != 2 {
			t.Errorf("期望 ReattachData(2 chunks)，收到 %v", msg.Payload)
		}
		t.Logf("Mobile 收到 ReattachData: start_seq=%d, chunks=%d", rd.StartSeq, len(rd.Chunks))
	case <-time.After(2 * time.Second):
		t.Fatal("Mobile 未收到 ReattachData")
	}

	t.Log("Reattach 增量补发流程测试通过")
}

// ── WebSocket 二进制帧测试 ──

func TestIntegration_WebSocketBinary(t *testing.T) {
	addr, devices, relay, _ := startServer(t)
	devices.Register("dev-ws", "tok", "1.0", "pc", "linux")

	agentCh := make(chan *fishttyv1.TunnelMessage, 8)
	relay.RegisterAgent("dev-ws", agentCh)
	defer relay.UnregisterAgent("dev-ws")

	wsURL := "ws" + strings.TrimPrefix(addr, "http") + "/ws?device_id=dev-ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{
		"Sec-WebSocket-Protocol": {"fish-tty-v1"},
	})
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer ws.Close()
	time.Sleep(100 * time.Millisecond)

	sessionID := "session-ws-1"

	// Mobile → Server → Agent
	initData, _ := proto.Marshal(&fishttyv1.TunnelMessage{
		SessionId: sessionID,
		Payload: &fishttyv1.TunnelMessage_SessionInit{
			SessionInit: &fishttyv1.SessionInit{SessionId: sessionID, Cols: 80, Rows: 24},
		},
	})
	_ = ws.WriteMessage(websocket.BinaryMessage, initData)

	select {
	case msg := <-agentCh:
		if msg.GetSessionInit() == nil {
			t.Error("期望 SessionInit")
		}
		t.Log("WS→Agent: SessionInit 路由成功")
	case <-time.After(2 * time.Second):
		t.Fatal("Agent 未收到消息")
	}

	// Agent → Server → Mobile
	relay.RouteFromAgent("dev-ws", &fishttyv1.TunnelMessage{
		SessionId: sessionID,
		Payload: &fishttyv1.TunnelMessage_SessionCreated{
			SessionCreated: &fishttyv1.SessionCreated{
				SessionId: sessionID, Status: fishttyv1.SessionStatus_SESSION_STATUS_OK,
			},
		},
	})
	relay.RouteFromAgent("dev-ws", &fishttyv1.TunnelMessage{
		SessionId: sessionID,
		Payload: &fishttyv1.TunnelMessage_DataChunk{
			DataChunk: &fishttyv1.DataChunk{Seq: 1, Data: []byte("hello from pty")},
		},
	})

	// 收 SessionCreated + DataChunk
	for i := 0; i < 2; i++ {
		_, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("WS 读取失败: %v", err)
		}
		var resp fishttyv1.TunnelMessage
		proto.Unmarshal(data, &resp)
		switch resp.Payload.(type) {
		case *fishttyv1.TunnelMessage_SessionCreated:
			t.Log("WS 收到 SessionCreated")
		case *fishttyv1.TunnelMessage_DataChunk:
			t.Logf("WS 收到 DataChunk: %s", string(resp.GetDataChunk().Data))
		}
	}

	t.Log("WebSocket 二进制帧 + Relay 路由测试通过")
}

// ── 并发安全测试 ──

func TestIntegration_ConcurrentConnections(t *testing.T) {
	_, devices, relay, _ := startServer(t)

	for i := 1; i <= 20; i++ {
		devices.Register(fmt.Sprintf("dev-%d", i), "tok", "1.0", "pc", "linux")
	}

	var wg sync.WaitGroup
	for i := 1; i <= 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ch := make(chan *fishttyv1.TunnelMessage, 4)
			relay.RegisterAgent(fmt.Sprintf("dev-%d", idx), ch)
		}(i)
	}
	wg.Wait()

	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ch := make(chan *fishttyv1.TunnelMessage, 4)
			relay.RegisterMobile(fmt.Sprintf("m-%d", idx), fmt.Sprintf("dev-%d", (idx%10)+1), ch)
		}(i)
	}
	wg.Wait()

	if relay.ConnectedAgentCount() != 10 || relay.ConnectedMobileCount() != 20 {
		t.Errorf("期望 10A+20M，实际=%dA+%dM", relay.ConnectedAgentCount(), relay.ConnectedMobileCount())
	}
	t.Logf("%d Agent + %d Mobile 并发注册安全", relay.ConnectedAgentCount(), relay.ConnectedMobileCount())
}
