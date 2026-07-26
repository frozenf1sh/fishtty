// fishtty-agent：家用 PC 后台守护进程。
//
// 配置优先级：命令行参数 > 环境变量 > 配置文件 > 默认值。
// 配置文件：fishtty-agent.yaml（当前目录、/etc/fishtty/、~/.config/fishtty/）
//
// 用法：
//
//	fishtty-agent --token my-secret-token
//	fishtty-agent --config /etc/fishtty/agent.yaml
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/frozenf1sh/fishpts/internal/agent/service"
	"github.com/frozenf1sh/fishpts/internal/config"
)

var Version = "dev"

func main() {
	var (
		server   = flag.String("server", "", "Server 地址（覆盖配置文件）")
		token    = flag.String("token", "", "认证令牌（覆盖配置文件/环境变量）")
		deviceID = flag.String("device-id", "", "设备 ID（覆盖配置文件/环境变量）")
		logLevel = flag.String("log-level", "", "日志级别")
		cfgFile  = flag.String("config", "", "配置文件路径")
		showVer  = flag.Bool("version", false, "显示版本号")
	)
	flag.Parse()
	if *showVer { fmt.Println("fishtty-agent", Version); os.Exit(0) }

	// ── 加载配置 ──
	cfg, err := config.LoadAgent(*cfgFile)
	if err != nil { fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err); os.Exit(1) }

	// 命令行覆盖配置文件 + 环境变量
	if *server != "" { cfg.Server = *server }
	if *token != "" { cfg.Token = *token }
	if *deviceID != "" { cfg.DeviceID = *deviceID }
	if *logLevel != "" { cfg.LogLevel = *logLevel }
	if cfg.Token == "" { cfg.Token = os.Getenv("FISHTTY_TOKEN") }
	if cfg.Token == "" { slog.Error("缺少认证令牌：通过 --token、FISHTTY_TOKEN 环境变量或配置文件"); os.Exit(1) }
	if cfg.DeviceID == "" { cfg.DeviceID = os.Getenv("FISHTTY_DEVICE_ID") }
	if cfg.DeviceID == "" { cfg.DeviceID = service.DefaultHostname() }

	hostname := service.DefaultHostname()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)})))
	slog.Info("fishtty-agent 启动", "version", Version, "server", cfg.Server, "device_id", cfg.DeviceID, "hostname", hostname)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sigCh; slog.Info("收到信号，正在优雅关闭"); cancel() }()

	tunnel := service.NewTunnelService(service.Config{
		ServerAddr: cfg.Server, DeviceID: cfg.DeviceID, Token: cfg.Token,
		AgentVer: Version, Hostname: hostname,
		Heartbeat: cfg.Heartbeat, Reconnect: cfg.Reconnect, RingBuffer: cfg.RingBuffer,
	})
	if err := tunnel.Run(ctx); err != nil && err != context.Canceled {
		slog.Error("Tunnel 异常退出", "error", err); os.Exit(1)
	}
	slog.Info("fishtty-agent 已退出")
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug": return slog.LevelDebug
	case "warn": return slog.LevelWarn
	case "error": return slog.LevelError
	default: return slog.LevelInfo
	}
}
