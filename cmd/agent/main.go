// fishtty-agent：家用 PC 后台守护进程。
//
// 启动时向 fishtty-server 发起反向长连接（Connect-RPC 双向流），
// 等待 Server/前端的 SessionInit 请求来创建 PTY 虚拟终端会话，
// 并通过环形缓冲区实现断连后的无缝重连（Reattach）。
//
// 用法：
//
//	fishtty-agent --server wss://fishtty.example.com --token <device-token>
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/frozenf1sh/fishpts/internal/agent"
)

// 编译时注入的版本号
var (
	Version = "dev"
)

func main() {
	// ── CLI flags ──
	var (
		serverAddr = flag.String("server", "https://localhost:8443", "fishtty-server 地址")
		token      = flag.String("token", "", "预共享设备认证令牌")
		deviceID   = flag.String("device-id", "", "设备唯一标识（留空则自动生成）")
		logLevel   = flag.String("log-level", "info", "日志级别: debug, info, warn, error")
		showVer    = flag.Bool("version", false, "显示版本号并退出")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("fishtty-agent", Version)
		os.Exit(0)
	}

	// ── 日志配置 ──
	level := parseLogLevel(*logLevel)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})))

	// ── 参数校验 ──
	if *token == "" {
		*token = os.Getenv("FISHTTY_TOKEN")
	}
	if *token == "" {
		slog.Error("缺少认证令牌：请通过 --token 或 FISHTTY_TOKEN 环境变量提供")
		os.Exit(1)
	}

	if *deviceID == "" {
		*deviceID = os.Getenv("FISHTTY_DEVICE_ID")
	}
	if *deviceID == "" {
		// 自动生成：使用主机名
		*deviceID = agent.DefaultHostname()
	}

	hostname := agent.DefaultHostname()

	slog.Info("fishtty-agent 启动",
		"version", Version,
		"server", *serverAddr,
		"device_id", *deviceID,
		"hostname", hostname,
	)

	// ── 信号处理 ──
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Info("收到信号，正在优雅关闭", "signal", sig)
		cancel()
	}()

	// ── 创建并运行 Tunnel ──
	tunnel := agent.NewTunnel(agent.TunnelConfig{
		ServerAddr: *serverAddr,
		DeviceID:   *deviceID,
		Token:      *token,
		AgentVer:   Version,
		Hostname:   hostname,
	})

	if err := tunnel.Run(ctx); err != nil && err != context.Canceled {
		slog.Error("Tunnel 异常退出", "error", err)
		os.Exit(1)
	}

	slog.Info("fishtty-agent 已退出")
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
