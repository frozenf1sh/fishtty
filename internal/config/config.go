// Package config 提供 fishtty 的统一配置管理。
// 支持 YAML 配置文件 + 环境变量覆盖 + 命令行参数，
// 优先级：命令行 > 环境变量 > 配置文件 > 默认值。
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// ── Server 配置 ──

// Server 包含 fishtty-server 的全部配置项。
type Server struct {
	Listen  string `mapstructure:"listen"`  // 监听地址，如 ":8443"
	TLSCert string `mapstructure:"tls_cert"` // TLS 证书路径（留空 = 明文 HTTP + h2c）
	TLSKey  string `mapstructure:"tls_key"`  // TLS 私钥路径
	LogLevel string `mapstructure:"log_level"` // debug | info | warn | error
	WebDir   string `mapstructure:"web_dir"`  // PWA 静态目录（留空 = 内嵌）
}

// DefaultServer 返回 Server 的默认配置。
func DefaultServer() Server {
	return Server{
		Listen:   ":8443",
		LogLevel: "info",
	}
}

// ── Agent 配置 ──

// Agent 包含 fishtty-agent 的全部配置项。
type Agent struct {
	Server   string `mapstructure:"server"`    // Server 地址，如 https://fishtty.example.com
	Token    string `mapstructure:"token"`     // 预共享认证令牌（必填）
	DeviceID string `mapstructure:"device_id"` // 设备唯一标识（留空 = 主机名）
	LogLevel string `mapstructure:"log_level"` // debug | info | warn | error

	Heartbeat  HeartbeatConfig  `mapstructure:"heartbeat"`
	Reconnect  ReconnectConfig  `mapstructure:"reconnect"`
	RingBuffer RingBufferConfig `mapstructure:"ring_buffer"`
}

// HeartbeatConfig 心跳参数。
type HeartbeatConfig struct {
	Interval      time.Duration `mapstructure:"interval"`
	MissThreshold int           `mapstructure:"miss_threshold"`
}

// ReconnectConfig 重连参数。
type ReconnectConfig struct {
	MinDelay   time.Duration `mapstructure:"min_delay"`
	MaxDelay   time.Duration `mapstructure:"max_delay"`
	ResetAfter time.Duration `mapstructure:"reset_after"`
}

// RingBufferConfig PTY 输出缓冲区参数。
type RingBufferConfig struct {
	SizeKB int `mapstructure:"size_kb"` // 环形缓冲区容量（KB）
}

// DefaultAgent 返回 Agent 的默认配置。
func DefaultAgent() Agent {
	return Agent{
		Server:   "https://localhost:8443",
		LogLevel: "info",
		Heartbeat: HeartbeatConfig{
			Interval:      15 * time.Second,
			MissThreshold: 3,
		},
		Reconnect: ReconnectConfig{
			MinDelay:   1 * time.Second,
			MaxDelay:   60 * time.Second,
			ResetAfter: 30 * time.Second,
		},
		RingBuffer: RingBufferConfig{
			SizeKB: 128,
		},
	}
}

// ── 加载函数 ──

// LoadServer 加载 Server 配置。
// 配置文件路径通过 --config 参数或 FISHTTY_CONFIG 环境变量指定。
func LoadServer(configPath string) (Server, error) {
	cfg := DefaultServer()
	if err := load("fishtty-server", configPath, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// LoadAgent 加载 Agent 配置。
func LoadAgent(configPath string) (Agent, error) {
	cfg := DefaultAgent()
	if err := load("fishtty-agent", configPath, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// ── 内部实现 ──

func load(name, configPath string, out interface{}) error {
	v := viper.New()
	v.SetConfigName(name)
	v.SetConfigType("yaml")

	// 默认配置路径：当前目录、/etc/fishtty/、$HOME/.config/fishtty/
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/fishtty")
	v.AddConfigPath("$HOME/.config/fishtty")

	if configPath != "" {
		v.SetConfigFile(configPath)
	}

	// 环境变量映射：FISHTTY_ → config key（自动将 . 转 _）
	v.SetEnvPrefix("FISHTTY")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// 先读配置文件（不存在不报错）
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("读取配置文件失败: %w", err)
		}
	}

	// 映射到 config struct
	if err := v.Unmarshal(out); err != nil {
		return fmt.Errorf("解析配置失败: %w", err)
	}

	return nil
}
