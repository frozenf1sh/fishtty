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
	"github.com/frozenf1sh/fishpts/internal/server"
)

var (
	Version = "dev"
)

// ── 路由前缀（不走 SPA fallback） ──

var apiPrefixes = []string{
	"/ws",
	"/metrics",
	"/fishtty.v1.FishTTY/",
}

func main() {
	// ── CLI flags ──
	var (
		listenAddr = flag.String("listen", ":8443", "监听地址")
		tlsCert    = flag.String("tls-cert", "", "TLS 证书文件路径")
		tlsKey     = flag.String("tls-key", "", "TLS 私钥文件路径")
		logLevel   = flag.String("log-level", "info", "日志级别: debug, info, warn, error")
		webDir     = flag.String("web-dir", "", "PWA 静态文件目录（覆盖内嵌的 web/dist）")
		showVer    = flag.Bool("version", false, "显示版本号并退出")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("fishtty-server", Version)
		os.Exit(0)
	}

	// ── 日志配置 ──
	level := parseLogLevel(*logLevel)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})))

	slog.Info("fishtty-server 启动", "version", Version, "listen", *listenAddr)

	// ── 初始化组件 ──
	devices := server.NewDeviceRegistry()
	relay := server.NewRelay(devices)
	sessionTracker := server.NewSessionTracker()

	// ── 注册 HTTP 路由 ──
	mux := http.NewServeMux()

	// Connect-RPC：Agent 隧道
	tunnelHandler := server.NewTunnelHandler(devices, relay)
	tunnelPath, tunnelHTTP := tunnelHandler.Handler()
	mux.Handle(tunnelPath, tunnelHTTP)
	slog.Info("Connect-RPC 隧道端点已注册", "path", tunnelPath)

	// WebSocket：Mobile 接入
	wsHandler := server.NewWSHandler(devices, relay)
	mux.Handle("/ws", wsHandler)
	slog.Info("WebSocket 端点已注册", "path", "/ws")

	// Prometheus 指标端点
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "# HELP fishtty_connected_agents 当前连接的 Agent 数量\n")
		fmt.Fprintf(w, "# TYPE fishtty_connected_agents gauge\n")
		fmt.Fprintf(w, "fishtty_connected_agents %d\n", relay.ConnectedAgentCount())
		fmt.Fprintf(w, "# HELP fishtty_connected_mobiles 当前连接的 Mobile 数量\n")
		fmt.Fprintf(w, "# TYPE fishtty_connected_mobiles gauge\n")
		fmt.Fprintf(w, "fishtty_connected_mobiles %d\n", relay.ConnectedMobileCount())
		fmt.Fprintf(w, "# HELP fishtty_active_sessions 当前活跃的 PTY 会话数\n")
		fmt.Fprintf(w, "# TYPE fishtty_active_sessions gauge\n")
		fmt.Fprintf(w, "fishtty_active_sessions %d\n", sessionTracker.Count())
		fmt.Fprintf(w, "# HELP fishtty_registered_devices 已注册设备总数\n")
		fmt.Fprintf(w, "# TYPE fishtty_registered_devices gauge\n")
		fmt.Fprintf(w, "fishtty_registered_devices %d\n", devices.Count())
		fmt.Fprintf(w, "# HELP fishtty_online_devices 在线设备数\n")
		fmt.Fprintf(w, "# TYPE fishtty_online_devices gauge\n")
		fmt.Fprintf(w, "fishtty_online_devices %d\n", devices.CountOnline())
	})
	slog.Info("Prometheus 指标端点已注册", "path", "/metrics")

	// PWA 静态文件（SPA fallback）
	var staticFS fs.FS
	if *webDir != "" {
		staticFS = os.DirFS(*webDir)
		slog.Info("PWA 静态文件使用外部目录", "dir", *webDir)
	} else {
		// 使用嵌入的 web/dist
		sub, err := fs.Sub(fishpts.WebDist, "web/dist")
		if err != nil {
			slog.Warn("内嵌 PWA 文件不可用（请先运行 cd web && pnpm build）", "error", err)
		} else {
			staticFS = sub
			slog.Info("PWA 静态文件使用内嵌资源")
		}
	}

	if staticFS != nil {
		mux.Handle("/", spaHandler(staticFS))
	}

	// ── HTTP Server（支持 HTTP/2 Cleartext） ──
	// 无 TLS 时启用 UnencryptedHTTP2，使 Connect-RPC 双向流在开发/内网环境中正常工作。
	protocols := &http.Protocols{}
	protocols.SetHTTP1(true)
	if *tlsCert == "" {
		protocols.SetUnencryptedHTTP2(true)
		slog.Info("已启用 UnencryptedHTTP2（HTTP/2 Cleartext）支持")
	} else {
		protocols.SetHTTP2(true)
	}

	srv := &http.Server{
		Addr:      *listenAddr,
		Handler:   mux,
		Protocols: protocols,
		ReadTimeout:       0,                       // 0 = 不限制，流式 RPC 必需
			ReadHeaderTimeout: 10 * time.Second,         // 仅限制 header 读取（防慢速攻击）
		WriteTimeout: 0, // WebSocket 长连接，不设写超时
		IdleTimeout:  120 * time.Second,
	}

	// ── 信号处理 ──
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Info("收到信号，正在关闭", "signal", sig)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("HTTP Server 关闭失败", "error", err)
		}
	}()

	// ── 启动 ──
	var serveErr error
	if *tlsCert != "" && *tlsKey != "" {
		slog.Info("使用 TLS", "cert", *tlsCert, "key", *tlsKey)
		serveErr = srv.ListenAndServeTLS(*tlsCert, *tlsKey)
	} else {
		slog.Warn("未配置 TLS，使用明文 HTTP（仅用于开发/内网）")
		serveErr = srv.ListenAndServe()
	}

	if serveErr != nil && serveErr != http.ErrServerClosed {
		slog.Error("HTTP Server 异常退出", "error", serveErr)
		os.Exit(1)
	}

	slog.Info("fishtty-server 已退出")
}

// ── SPA fallback 处理器 ──
//
// 对于非 API/WS 路由，返回 index.html，由前端 React Router 处理客户端路由。
// API/WS 路由（以 /ws、/metrics、/fishtty.v1. 开头）由各自的 handler 处理。

func spaHandler(fileFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fileFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API/WS 路由不处理
		for _, prefix := range apiPrefixes {
			if strings.HasPrefix(r.URL.Path, prefix) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		}

		// 尝试直接提供文件
		path := strings.TrimPrefix(r.URL.Path, "/")
		f, err := fileFS.Open(path)
		if err != nil {
			// 文件不存在 → SPA fallback：返回 index.html
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		f.Close()

		// 文件存在 → 直接提供（带缓存头）
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, r)
	})
}

// parseLogLevel 将字符串转换为 slog.Level。
func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
