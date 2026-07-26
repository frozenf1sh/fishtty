// fishtty-server：公网中继服务。
//
// 接受 Agent 的 Connect-RPC 反向隧道连接和 Mobile 的 WebSocket 连接，
// 通过 Relay 在两者之间双向转发 TunnelMessage。
// 内置嵌入式 PWA 静态文件（web/dist），同时支持 SPA 路由 fallback。
//
// 用法：
//
//	fishtty-server --listen :8443 --tls-cert server.crt --tls-key server.key
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	fishpts "github.com/frozenf1sh/fishpts"
	"github.com/frozenf1sh/fishpts/internal/server/adapter/connectrpc"
	wspkg "github.com/frozenf1sh/fishpts/internal/server/adapter/websocket"
	"github.com/frozenf1sh/fishpts/internal/server/service"
)

var Version = "dev"
var apiPrefixes = []string{"/ws", "/metrics", "/fishtty.v1.FishTTY/"}

func main() {
	var (
		listenAddr = flag.String("listen", ":8443", "监听地址")
		tlsCert    = flag.String("tls-cert", "", "TLS 证书文件路径")
		tlsKey     = flag.String("tls-key", "", "TLS 私钥文件路径")
		logLevel   = flag.String("log-level", "info", "日志级别: debug, info, warn, error")
		webDir     = flag.String("web-dir", "", "PWA 静态文件目录（覆盖内嵌）")
		showVer    = flag.Bool("version", false, "显示版本号并退出")
	)
	flag.Parse()
	if *showVer { fmt.Println("fishtty-server", Version); os.Exit(0) }

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(*logLevel)})))
	slog.Info("fishtty-server 启动", "version", Version, "listen", *listenAddr)

	// ── 初始化依赖 ──
	devices := service.NewDeviceRegistry()
	relay := service.NewRelay()

	mux := http.NewServeMux()

	// Connect-RPC：Agent 隧道
	tunnelH := connectrpc.NewHandler(devices, relay)
	tunnelPath, tunnelHTTP := tunnelH.Route()
	mux.Handle(tunnelPath, tunnelHTTP)

	// WebSocket：Mobile 接入
	mux.Handle("/ws", wspkg.NewHandler(devices, relay))

	// Prometheus 指标
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "fishtty_connected_agents %d\n", relay.AgentCount())
		fmt.Fprintf(w, "fishtty_connected_mobiles %d\n", relay.MobileCount())
		fmt.Fprintf(w, "fishtty_active_sessions %d\n", relay.SessionCount())
		fmt.Fprintf(w, "fishtty_registered_devices %d\n", devices.Count())
		fmt.Fprintf(w, "fishtty_online_devices %d\n", devices.CountOnline())
	})

	// PWA 静态文件
	var staticFS fs.FS
	if *webDir != "" {
		staticFS = os.DirFS(*webDir)
	} else if sub, err := fs.Sub(fishpts.WebDist, "web/dist"); err == nil {
		staticFS = sub
	}
	if staticFS != nil { mux.Handle("/", spaHandler(staticFS)) }

	// ── HTTP Server ──
	protocols := &http.Protocols{}
	protocols.SetHTTP1(true)
	if *tlsCert == "" { protocols.SetUnencryptedHTTP2(true) } else { protocols.SetHTTP2(true) }

	srv := &http.Server{
		Addr: *listenAddr, Handler: mux, Protocols: protocols,
		ReadTimeout: 0, ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout: 0, IdleTimeout: 120 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh; slog.Info("收到信号，正在关闭")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	var serveErr error
	if *tlsCert != "" && *tlsKey != "" {
		serveErr = srv.ListenAndServeTLS(*tlsCert, *tlsKey)
	} else {
		slog.Warn("未配置 TLS，使用明文 HTTP（仅用于开发/内网）")
		serveErr = srv.ListenAndServe()
	}
	if serveErr != nil && serveErr != http.ErrServerClosed { slog.Error("Server 异常退出", "error", serveErr); os.Exit(1) }
	slog.Info("fishtty-server 已退出")
}

// ── SPA fallback ──

func spaHandler(fileFS fs.FS) http.Handler {
	fs := http.FileServer(http.FS(fileFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range apiPrefixes { if strings.HasPrefix(r.URL.Path, p) { w.WriteHeader(http.StatusNotFound); return } }
		path := strings.TrimPrefix(r.URL.Path, "/")
		f, err := fileFS.Open(path)
		if err != nil { r.URL.Path = "/"; fs.ServeHTTP(w, r); return }
		f.Close()
		if strings.HasPrefix(path, "assets/") { w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") }
		fs.ServeHTTP(w, r)
	})
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug": return slog.LevelDebug
	case "warn": return slog.LevelWarn
	case "error": return slog.LevelError
	default: return slog.LevelInfo
	}
}
