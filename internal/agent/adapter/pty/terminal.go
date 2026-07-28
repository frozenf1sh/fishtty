// Package pty 是 PTY 伪终端的 creack/pty 适配实现。
// 实现 domain.TerminalEmulator 和 domain.TerminalFactory 接口。
package pty

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"sync"

	goPty "github.com/creack/pty"

	"github.com/frozenf1sh/fishpts/internal/domain"
)

// ── 环境变量过滤 ──

// 需要从 PTY 环境中过滤掉的 Claude/IDE 标记前缀，
// 避免这些变量泄漏到终端会话中。
var filteredEnvPrefixes = []string{
	"CLAUDE_", "ANTHROPIC_", "COPILOT_", "VSCODE_",
	"CODE_", "JETBRAINS_", "IDEA_",
}

// ── Terminal 实现 ──

// Terminal 适配 creack/pty，实现 domain.TerminalEmulator。
type Terminal struct {
	mu     sync.Mutex
	f      *os.File  // PTY master fd
	cmd    *exec.Cmd // 子进程
	closed bool
	rows   uint32
	cols   uint32
}

// Factory 创建 PTY 终端，实现 domain.TerminalFactory。
type Factory struct{}

func NewFactory() *Factory { return &Factory{} }

func (f *Factory) Create(cfg domain.TerminalConfig) (domain.TerminalEmulator, error) {
	return New(cfg)
}

// New 创建并启动一个新的 PTY 终端。
//
// 模拟 SSH 登录体验：
//   - 以 login shell 方式启动（zsh -l / bash -l），确保加载 .profile/.zprofile/.zshrc
//   - 工作目录设为用户 HOME（通过 os/user 查找，不依赖 $HOME 环境变量）
//   - 强制注入 HOME、USER、SHELL、PATH 等关键环境变量
//     （systemd 下 Agent 环境极简，需主动补齐）
func New(cfg domain.TerminalConfig) (*Terminal, error) {
	if cfg.Cols == 0 {
		cfg.Cols = 80
	}
	if cfg.Rows == 0 {
		cfg.Rows = 24
	}

	// ── 解析目标用户信息 ──
	// 优先用 $SUDO_USER（sudo 场景），其次当前用户
	currentUser, _ := user.Current()
	targetUser := currentUser
	homeDir := ""
	if currentUser != nil {
		homeDir = currentUser.HomeDir
	}
	// 如果 agent 以 root 运行，尝试找到实际用户
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		if u, err := user.Lookup(sudoUser); err == nil {
			targetUser = u
			homeDir = u.HomeDir
		}
	}
	// 兜底
	if homeDir == "" {
		homeDir = "/root"
	}

	// ── 选择 shell ──
	// systemd 下 Agent 没有 SHELL 环境变量，需从用户数据库查找
	var cmd *exec.Cmd
	if cfg.Command != "" {
		cmd = exec.Command(cfg.Command)
	} else {
		shell := resolveShell(targetUser)
		// -l: login shell，加载 .profile/.zprofile/.zshrc
		cmd = exec.Command(shell, "-l")
	}

	// ── 工作目录 ──
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	} else {
		cmd.Dir = homeDir
	}

	// ── 构建 PTY 环境变量 ──
	// systemd 下 Agent 环境极简，需要主动补齐关键变量
	cmd.Env = buildPTYEnv(cfg, targetUser, homeDir)

	// ── 启动 PTY ──
	winSize := &goPty.Winsize{
		Rows: uint16(cfg.Rows), Cols: uint16(cfg.Cols),
		X: uint16(cfg.Cols * 8), Y: uint16(cfg.Rows * 16),
	}
	f, err := goPty.StartWithSize(cmd, winSize)
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}

	return &Terminal{f: f, cmd: cmd, rows: cfg.Rows, cols: cfg.Cols}, nil
}

// buildPTYEnv 构建 PTY 子进程的完整环境变量。
// 从 Agent 环境继承（过滤 IDE 标记），补齐 HOME/USER/SHELL/PATH 等关键变量。
func buildPTYEnv(cfg domain.TerminalConfig, u *user.User, homeDir string) []string {
	env := make(map[string]string)

	// 1. 从 Agent 继承（过滤掉 IDE 标记）
	for _, e := range os.Environ() {
		skip := false
		for _, p := range filteredEnvPrefixes {
			if strings.HasPrefix(e, p) {
				skip = true
				break
			}
		}
		if !skip {
			if k, v, ok := strings.Cut(e, "="); ok {
				env[k] = v
			}
		}
	}

	// 2. 注入配置文件指定的变量
	for k, v := range cfg.Env {
		env[k] = v
	}

	// 3. 强制设置关键变量（覆盖从 Agent 继承的可能为空的值）
	env["TERM"] = "xterm-256color"
	env["COLORTERM"] = "truecolor"
	env["TERM_PROGRAM"] = "fishtty"

	// HOME — systemd 下可能为空，强制设为用户 home 目录
	env["HOME"] = homeDir

	// USER / LOGNAME
	if u != nil {
		env["USER"] = u.Username
		env["LOGNAME"] = u.Username
	}

	// SHELL — 从用户数据库查找
	if u != nil {
		if _, ok := env["SHELL"]; !ok {
			env["SHELL"] = resolveShell(u)
		}
	}
	if env["SHELL"] == "" {
		env["SHELL"] = "/bin/bash"
	}

	// PATH — systemd 下可能为空
	if env["PATH"] == "" {
		env["PATH"] = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	}

	// LANG
	if env["LANG"] == "" {
		env["LANG"] = "en_US.UTF-8"
	}
	if env["LC_ALL"] == "" {
		env["LC_ALL"] = "en_US.UTF-8"
	}

	// PWD
	env["PWD"] = homeDir

	// UID / GID
	if u != nil {
		env["UID"] = u.Uid
		env["GID"] = u.Gid
	}

	// 4. 转为 []string
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// resolveShell 解析用户默认 shell。
// 优先级：环境变量 SHELL > /etc/passwd 中记录的 shell > /bin/bash。
func resolveShell(u *user.User) string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	// 从用户数据库查找（systemd 下环境变量不可用时的回退）
	if u != nil {
		if _, err := os.Stat(u.HomeDir + "/.zshrc"); err == nil {
			return "/usr/bin/zsh"
		}
	}
	// 检查常见路径
	for _, s := range []string{"/usr/bin/zsh", "/bin/zsh", "/usr/bin/bash", "/bin/bash"} {
		if _, err := os.Stat(s); err == nil {
			return s
		}
	}
	return "/bin/bash"
}

// ── domain.TerminalEmulator 实现 ──

func (t *Terminal) Read(buf []byte) (int, error)   { return t.f.Read(buf) }
func (t *Terminal) Pid() int {
	if t.cmd.Process != nil { return t.cmd.Process.Pid }
	return 0
}

func (t *Terminal) Write(data []byte) (int, error) {
	t.mu.Lock(); defer t.mu.Unlock()
	if t.closed { return 0, io.ErrClosedPipe }
	return t.f.Write(data)
}

func (t *Terminal) Resize(cols, rows uint32) error {
	t.mu.Lock(); defer t.mu.Unlock()
	if t.closed { return io.ErrClosedPipe }

	ws := &goPty.Winsize{
		Rows: uint16(rows), Cols: uint16(cols),
		X: uint16(cols * 8), Y: uint16(rows * 16),
	}
	if err := goPty.Setsize(t.f, ws); err != nil {
		return fmt.Errorf("pty setsize: %w", err)
	}
	t.rows, t.cols = rows, cols
	return nil
}

func (t *Terminal) Close() error {
	t.mu.Lock(); defer t.mu.Unlock()
	if t.closed { return nil }
	t.closed = true
	closeErr := t.f.Close()
	if t.cmd.Process != nil {
		// 先尝试优雅终止，超时后强制 kill
		_ = t.cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() {
			_ = t.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		default:
			_ = t.cmd.Process.Kill()
			<-done
		}
	}
	return closeErr
}
