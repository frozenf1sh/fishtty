// Package websocket 提供 Mobile/Web PWA 的 WebSocket 接入适配器。
package websocket

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync/atomic"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	"github.com/frozenf1sh/fishpts/internal/domain"
	"github.com/frozenf1sh/fishpts/internal/server/auth"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	subProtocol = "fish-tty-v1"
	readLimit   = 512 * 1024
)

var (
	upgrader = websocket.Upgrader{
		ReadBufferSize: 4096, WriteBufferSize: 4096,
		CheckOrigin:  func(r *http.Request) bool { return true },
		Subprotocols: []string{subProtocol}, EnableCompression: true,
	}
	connIDSeq atomic.Uint64
)

// Handler 处理 WebSocket 连接。
type Handler struct {
	devices domain.DeviceStore
	relay   domain.RelayRouter
	logger  *slog.Logger
}

// NewHandler 创建 WS handler。
func NewHandler(devices domain.DeviceStore, relay domain.RelayRouter) *Handler {
	return &Handler{devices: devices, relay: relay, logger: slog.Default().With("component", "ws_handler")}
}

// ServeHTTP 实现 http.Handler。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	if deviceID == "" { http.Error(w, "缺少 device_id", http.StatusBadRequest); return }
	if err := auth.Mobile(h.devices, deviceID); err != nil {
		http.Error(w, fmt.Sprintf("认证失败: %v", err), http.StatusUnauthorized); return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil { h.logger.Error("WS 升级失败", "error", err); return }
	conn.SetReadLimit(readLimit)

	connID := fmt.Sprintf("mobile-%d", connIDSeq.Add(1))
	logger := h.logger.With("conn_id", connID, "device_id", deviceID)
	logger.Info("Mobile WebSocket 已连接")

	// 注册到中继
	wsSender := &wsSender{conn: conn, logger: logger}
	h.relay.RegisterMobile(connID, deviceID, wsSender)
	defer h.relay.UnregisterMobile(connID)

	// drainLoop：relay → WS
	ch := make(chan *fishttyv1.TunnelMessage, 256)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer func() { if r := recover(); r != nil { logger.Error("wsDrain panic", "panic", r, "stack", string(debug.Stack())) } }()
		for {
			select {
			case <-ctx.Done(): return
			case msg, ok := <-ch:
				if !ok { return }
				data, err := proto.Marshal(msg)
				if err != nil { logger.Warn("序列化失败", "error", err); continue }
				if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil { logger.Warn("WS 写入失败", "error", err); return }
			}
		}
	}()

	// 读循环
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil { break }
		if mt != websocket.BinaryMessage { continue }
		var msg fishttyv1.TunnelMessage
		if err := proto.Unmarshal(data, &msg); err != nil { logger.Warn("反序列化失败", "error", err); continue }
		h.relay.RouteFromMobile(connID, &msg)
	}
	conn.Close()
	logger.Info("Mobile WebSocket 已断开")
}

// wsSender 实现 domain.MessageSender。
type wsSender struct {
	conn   *websocket.Conn
	logger *slog.Logger
}

func (s *wsSender) SendMessage(msg *fishttyv1.TunnelMessage) error {
	data, err := proto.Marshal(msg)
	if err != nil { return err }
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}
