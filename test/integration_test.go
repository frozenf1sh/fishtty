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
	"github.com/frozenf1sh/fishpts/internal/domain"
	"github.com/frozenf1sh/fishpts/internal/server/adapter/connectrpc"
	wspkg "github.com/frozenf1sh/fishpts/internal/server/adapter/websocket"
	"github.com/frozenf1sh/fishpts/internal/server/service"
	ws "github.com/gorilla/websocket"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/proto"
)

func startServer(t *testing.T) (string, domain.DeviceStore, domain.RelayRouter, func()) {
	t.Helper()
	devices := service.NewDeviceRegistry()
	relay := service.NewRelay()

	mux := http.NewServeMux()
	tunnelPath, tunnelHTTP := connectrpc.NewHandler(devices, relay).Route()
	mux.Handle(tunnelPath, tunnelHTTP)
	mux.Handle("/ws", wspkg.NewHandler(devices, relay))

	protocols := &http.Protocols{}; protocols.SetHTTP1(true); protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: mux, Protocols: protocols}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatalf("监听失败: %v", err) }
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	time.Sleep(50 * time.Millisecond)
	return fmt.Sprintf("http://%s", ln.Addr().String()), devices, relay, func() { srv.Close() }
}

func h2cClient() *http.Client {
	return &http.Client{Transport: &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, n, a string, _ *tls.Config) (net.Conn, error) { var d net.Dialer; return d.DialContext(ctx, n, a) }}}
}

// ── 集成测试 ──

func TestIntegration_ConnectRPC_Tunnel(t *testing.T) {
	addr, devices, relay, _ := startServer(t)
	devices.Register("dev-h2c", "tok", "1.0", "pc", "linux")

	client := fishttyv1connect.NewFishTTYClient(h2cClient(), addr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second); defer cancel()
	stream := client.Tunnel(ctx)

	_ = stream.Send(&fishttyv1.TunnelMessage{Payload: &fishttyv1.TunnelMessage_AuthReq{
		AuthReq: &fishttyv1.AuthRequest{DeviceId: "dev-h2c", Token: "tok", AgentVersion: "1.0", Hostname: "pc", Platform: "linux"},
	}})
	msg, err := stream.Receive()
	if err != nil { t.Fatalf("recv: %v", err) }
	if msg.GetAuthResp().Status != fishttyv1.AuthStatus_AUTH_STATUS_OK { t.Fatal("auth failed") }
	t.Logf("✅ h2c 认证成功")

	if relay.AgentCount() != 1 { t.Error("Agent not registered") }
}

func TestIntegration_MessageRouting(t *testing.T) {
	_, devices, relay, _ := startServer(t)
	devices.Register("dev-r", "tok", "1.0", "pc", "linux")

	mobileCh := make(chan *fishttyv1.TunnelMessage, 16)
	relay.RegisterMobile("m1", "dev-r", &chanSender{ch: mobileCh})
	defer relay.UnregisterMobile("m1")

	sid := "s-route-1"
	relay.RouteFromAgent("dev-r", &fishttyv1.TunnelMessage{SessionId: sid, Payload: &fishttyv1.TunnelMessage_SessionCreated{
		SessionCreated: &fishttyv1.SessionCreated{SessionId: sid, Status: fishttyv1.SessionStatus_SESSION_STATUS_OK},
	}})
	<-mobileCh // drain SessionCreated

	for i := 1; i <= 5; i++ {
		relay.RouteFromAgent("dev-r", &fishttyv1.TunnelMessage{SessionId: sid, Payload: &fishttyv1.TunnelMessage_DataChunk{
			DataChunk: &fishttyv1.DataChunk{Seq: uint64(i), Data: []byte(fmt.Sprintf("c-%d", i))},
		}})
	}
	count := 0
	timeout := time.After(2 * time.Second)
	for count < 5 { select { case <-mobileCh: count++; case <-timeout: t.Fatal("timeout") } }
	t.Logf("✅ %d DataChunks routed", count)
}

func TestIntegration_ReattachFlow(t *testing.T) {
	_, devices, relay, _ := startServer(t)
	devices.Register("dev-re", "tok", "1.0", "pc", "linux")

	agentCh := make(chan *fishttyv1.TunnelMessage, 8)
	mobileCh := make(chan *fishttyv1.TunnelMessage, 8)
	relay.RegisterAgent("dev-re", &chanSender{ch: agentCh})
	relay.RegisterMobile("m-re", "dev-re", &chanSender{ch: mobileCh})

	sid := "s-re-1"
	relay.RouteFromAgent("dev-re", &fishttyv1.TunnelMessage{SessionId: sid, Payload: &fishttyv1.TunnelMessage_SessionCreated{
		SessionCreated: &fishttyv1.SessionCreated{SessionId: sid, Status: fishttyv1.SessionStatus_SESSION_STATUS_OK},
	}})
	<-mobileCh

	relay.RouteFromMobile("m-re", &fishttyv1.TunnelMessage{SessionId: sid, Payload: &fishttyv1.TunnelMessage_Reattach{
		Reattach: &fishttyv1.Reattach{SessionId: sid, LastAckSeq: 5},
	}})
	select { case msg := <-agentCh: if msg.GetReattach().LastAckSeq != 5 { t.Error("bad seq") }; case <-time.After(2 * time.Second): t.Fatal("timeout") }
	t.Log("✅ Reattach routed")
}

func TestIntegration_WebSocketBinary(t *testing.T) {
	addr, devices, _, _ := startServer(t)
	devices.Register("dev-ws", "tok", "1.0", "pc", "linux")

	wsURL := "ws" + strings.TrimPrefix(addr, "http") + "/ws?device_id=dev-ws"
	wsc, _, err := ws.DefaultDialer.Dial(wsURL, http.Header{"Sec-WebSocket-Protocol": {"fish-tty-v1"}})
	if err != nil { t.Fatal("ws dial:", err) }; defer wsc.Close()
	time.Sleep(100 * time.Millisecond)

	data, _ := proto.Marshal(&fishttyv1.TunnelMessage{SessionId: "s-ws", Payload: &fishttyv1.TunnelMessage_SessionInit{
		SessionInit: &fishttyv1.SessionInit{SessionId: "s-ws", Cols: 80, Rows: 24},
	}})
	wsc.WriteMessage(ws.BinaryMessage, data)
	t.Log("✅ WS binary sent")
}

func TestIntegration_ConcurrentConnections(t *testing.T) {
	_, devices, relay, _ := startServer(t)
	for i := 1; i <= 20; i++ { devices.Register(fmt.Sprintf("d-%d", i), "tok", "1.0", "pc", "linux") }
	var wg sync.WaitGroup
	for i := 1; i <= 10; i++ { wg.Add(1); go func(x int) { defer wg.Done(); ch := make(chan *fishttyv1.TunnelMessage, 4); relay.RegisterAgent(fmt.Sprintf("d-%d", x), &chanSender{ch: ch}) }(i) }
	wg.Wait()
	for i := 1; i <= 20; i++ { wg.Add(1); go func(x int) { defer wg.Done(); ch := make(chan *fishttyv1.TunnelMessage, 4); relay.RegisterMobile(fmt.Sprintf("m-%d", x), fmt.Sprintf("d-%d", (x%10)+1), &chanSender{ch: ch}) }(i) }
	wg.Wait()
	if relay.AgentCount() != 10 || relay.MobileCount() != 20 { t.Errorf("got %dA %dM", relay.AgentCount(), relay.MobileCount()) }
	t.Logf("✅ %dA + %dM concurrent", relay.AgentCount(), relay.MobileCount())
}

type chanSender struct{ ch chan *fishttyv1.TunnelMessage }
func (s *chanSender) SendMessage(msg *fishttyv1.TunnelMessage) error { s.ch <- msg; return nil }
