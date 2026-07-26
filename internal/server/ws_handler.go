package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync/atomic"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

// ── WebSocket 接入端 ──
//
// 处理来自移动端/Web PWA 的 WebSocket 连接。
// 使用二进制帧（Binary Message）传输 Protobuf 序列化的 TunnelMessage。
// 子协议：fish-tty-v1。

const (
	// wsSubProtocol WebSocket 子协议名称。
	wsSubProtocol = "fish-tty-v1"

	// wsSendChSize 发往 WebSocket 的通道缓冲区大小。
	wsSendChSize = 256

	// wsReadLimit 单条消息最大字节数（Protobuf 反序列化上限）。
	wsReadLimit = 512 * 1024 // 512 KB
)

var (
	// wsUpgrader 升级 HTTP 到 WebSocket。
	wsUpgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		// 生产环境应限制 Origin；v1 允许所有来源
		CheckOrigin:     func(r *http.Request) bool { return true },
		Subprotocols:    []string{wsSubProtocol},
		EnableCompression: true,
	}

	// connIDCounter 用于生成唯一 Mobile 连接 ID。
	connIDCounter atomic.Uint64
)

// WSHandler 处理 WebSocket 连接。
type WSHandler struct {
	devices *DeviceRegistry
	relay   *Relay
	logger  *slog.Logger
}

// NewWSHandler 创建 WSHandler。
func NewWSHandler(devices *DeviceRegistry, relay *Relay) *WSHandler {
	return &WSHandler{
		devices: devices,
		relay:   relay,
		logger:  slog.Default().With("component", "ws_handler"),
	}
}

// ServeHTTP 实现 http.Handler 接口。
// 路径：/ws?device_id=<device_id>
func (h *WSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// ── 认证 ──
	deviceID := r.URL.Query().Get("device_id")
	if deviceID == "" {
		http.Error(w, "缺少 device_id 参数", http.StatusBadRequest)
		return
	}

	_, err := AuthenticateMobile(h.devices, deviceID)
	if err != nil {
		http.Error(w, fmt.Sprintf("认证失败: %v", err), http.StatusUnauthorized)
		return
	}

	// ── 升级到 WebSocket ──
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("WebSocket 升级失败", "error", err)
		return
	}

	// 协商子协议
	if conn.Subprotocol() != wsSubProtocol {
		h.logger.Warn("客户端未协商子协议，继续处理",
			"got", conn.Subprotocol(),
			"expected", wsSubProtocol,
		)
	}

	// 设置读取限制
	conn.SetReadLimit(wsReadLimit)

	connID := fmt.Sprintf("mobile-%d", connIDCounter.Add(1))
	logger := h.logger.With("conn_id", connID, "device_id", deviceID)
	logger.Info("Mobile WebSocket 已连接")

	// ── 注册到 Relay ──
	sendCh := make(chan *fishttyv1.TunnelMessage, wsSendChSize)
	h.relay.RegisterMobile(connID, deviceID, sendCh)
	defer h.relay.UnregisterMobile(connID)

	// ── 运行双 goroutine ──
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// writeLoop：从 sendCh 读取 Relay 转发的消息 → 二进制帧写入 WS
	go h.writeLoop(ctx, conn, sendCh, logger)

	// readLoop：从 WS 读取二进制帧 → 交给 Relay 路由
	h.readLoop(ctx, conn, connID, logger)

	// readLoop 返回时连接已断开
	conn.Close()
	logger.Info("Mobile WebSocket 已断开")
}

// readLoop 从 WebSocket 读取二进制帧，反序列化后交给 Relay。
func (h *WSHandler) readLoop(ctx context.Context, conn *websocket.Conn, connID string, logger *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("readLoop panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				logger.Warn("读取 WebSocket 消息失败", "error", err)
			}
			return
		}

		// 仅接受二进制帧
		if messageType != websocket.BinaryMessage {
			logger.Warn("收到非二进制帧，已忽略", "type", messageType)
			continue
		}

		// 反序列化 Protobuf
		var msg fishttyv1.TunnelMessage
		if err := proto.Unmarshal(data, &msg); err != nil {
			logger.Warn("Protobuf 反序列化失败", "error", err, "raw_len", len(data))
			continue
		}

		// 交给 Relay 路由给 Agent
		h.relay.RouteFromMobile(connID, &msg)
	}
}

// writeLoop 从 sendCh 读取消息，序列化后以二进制帧写入 WebSocket。
func (h *WSHandler) writeLoop(ctx context.Context, conn *websocket.Conn, sendCh <-chan *fishttyv1.TunnelMessage, logger *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("writeLoop panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-sendCh:
			if !ok {
				return
			}

			data, err := proto.Marshal(msg)
			if err != nil {
				logger.Warn("Protobuf 序列化失败", "error", err)
				continue
			}

			if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				logger.Warn("写入 WebSocket 失败", "error", err)
				return
			}
		}
	}
}
