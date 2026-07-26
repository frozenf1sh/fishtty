// fishtty-server：公网中继服务。
//
// 配置优先级：命令行参数 > 环境变量 > 配置文件 > 默认值。
// 配置文件：fishtty-server.yaml（当前目录、/etc/fishtty/、~/.config/fishtty/）
//
// 用法：
//
//	fishtty-server                                    # 全部默认
//	fishtty-server --listen :8080 --log-level debug   # 命令行覆盖
//	fishtty-server --config /path/to/server.yaml      # 指定配置文件
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
	"github.com/frozenf1sh/fishpts/internal/config"
	"github.com/frozenf1sh/fishpts/internal/server/adapter/connectrpc"
	wspkg "github.com/frozenf1sh/fishpts/internal/server/adapter/websocket"
	"github.com/frozenf1sh/fishpts/internal/server/service"
)

var Version = "dev"
var apiPrefixes = []string{"/ws", "/metrics", "/fishtty.v1.FishTTY/"}

func main() {
	// ── 命令行参数（覆盖配置文件） ──
	var (
		listen   = flag.String("listen", "", "监听地址（覆盖配置文件）")
		tlsCert  = flag.String("tls-cert", "", "TLS 证书路径")
		tlsKey   = flag.String("tls-key", "", "TLS 私钥路径")
		logLevel = flag.String("log-level", "", "日志级别")
		webDir   = flag.String("web-dir", "", "PWA 静态文件目录")
		cfgFile  = flag.String("config", "", "配置文件路径")
		showVer  = flag.Bool("version", false, "显示版本号并退出")
	)
	flag.Parse()
	if *showVer { fmt.Println("fishtty-server", Version); os.Exit(0) }

	// ── 加载配置 ──
	cfg, err := config.LoadServer(*cfgFile)
	if err != nil { fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err); os.Exit(1) }

	// 命令行覆盖配置文件
	if *listen != "" { cfg.Listen = *listen }
	if *tlsCert != "" { cfg.TLSCert = *tlsCert }
	if *tlsKey != "" { cfg.TLSKey = *tlsKey }
	if *logLevel != "" { cfg.LogLevel = *logLevel }
	if *webDir != "" { cfg.WebDir = *webDir }

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)})))
	slog.Info("fishtty-server 启动", "version", Version, "listen", cfg.Listen)

	// ── 初始化依赖 ──
	devices := service.NewDeviceRegistry()
	relay := service.NewRelay()
	mux := http.NewServeMux()

	tunnelPath, tunnelHTTP := connectrpc.NewHandler(devices, relay).Route()
	mux.Handle(tunnelPath, tunnelHTTP)
	mux.Handle("/ws", wspkg.NewHandler(devices, relay))
	mux.HandleFunc("/metrics", metricsHandler(devices, relay))

	var staticFS fs.FS
	if cfg.WebDir != "" {
		staticFS = os.DirFS(cfg.WebDir)
	} else if sub, err := fs.Sub(fishpts.WebDist, "web/dist"); err == nil {
		staticFS = sub
	}
	if staticFS != nil { mux.Handle("/", spaHandler(staticFS)) }

	// ── HTTP Server ──
	protocols := &http.Protocols{}
	protocols.SetHTTP1(true)
	if cfg.TLSCert == "" { protocols.SetUnencryptedHTTP2(true) } else { protocols.SetHTTP2(true) }

	srv := &http.Server{
		Addr: cfg.Listen, Handler: mux, Protocols: protocols,
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
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		slog.Info("使用 TLS", "cert", cfg.TLSCert, "key", cfg.TLSKey)
		serveErr = srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
	} else {
		slog.Warn("未配置 TLS，使用明文 HTTP（仅用于开发/内网）")
		serveErr = srv.ListenAndServe()
	}
	if serveErr != nil && serveErr != http.ErrServerClosed { slog.Error("Server 异常退出", "error", serveErr); os.Exit(1) }
	slog.Info("fishtty-server 已退出")
}

// ── 辅助 ──

func metricsHandler(devices *service.DeviceRegistry, relay *service.Relay) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "fishtty_connected_agents %d\nfishtty_connected_mobiles %d\nfishtty_active_sessions %d\nfishtty_registered_devices %d\nfishtty_online_devices %d\n",
			relay.AgentCount(), relay.MobileCount(), relay.SessionCount(), devices.Count(), devices.CountOnline())
	}
}

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
