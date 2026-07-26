// Package pty 是 PTY 伪终端的 creack/pty 适配实现。
// 实现 domain.TerminalEmulator 和 domain.TerminalFactory 接口。
package pty

import (
	"fmt"
	"io"
	"os"
	"os/exec"
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
// 如果 cfg.Command 为空，启动用户默认 shell。
func New(cfg domain.TerminalConfig) (*Terminal, error) {
	if cfg.Cols == 0 {
		cfg.Cols = 80
	}
	if cfg.Rows == 0 {
		cfg.Rows = 24
	}

	// 选择 shell
	cmd := exec.Command(cfg.Command)
	if cfg.Command == "" {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		cmd = exec.Command(shell)
	}

	// 环境变量：继承 Agent 环境，过滤 IDE 标记，注入终端变量
	cmd.Env = filterEnv(os.Environ())
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Env = append(cmd.Env,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"LANG=en_US.UTF-8",
		"LC_ALL=en_US.UTF-8",
		"TERM_PROGRAM=fishtty",
	)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}

	// 启动 PTY
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
	ws := &goPty.Winsize{Rows: uint16(rows), Cols: uint16(cols), X: uint16(cols * 8), Y: uint16(rows * 16)}
	if err := goPty.Setsize(t.f, ws); err != nil { return fmt.Errorf("pty setsize: %w", err) }
	t.rows, t.cols = rows, cols
	return nil
}

func (t *Terminal) Close() error {
	t.mu.Lock(); defer t.mu.Unlock()
	if t.closed { return nil }
	t.closed = true
	closeErr := t.f.Close()
	if t.cmd.Process != nil { _ = t.cmd.Wait() }
	return closeErr
}

// ── 环境过滤 ──

func filterEnv(environ []string) []string {
	var out []string
	for _, e := range environ {
		skip := false
		for _, p := range filteredEnvPrefixes {
			if len(e) >= len(p) && e[:len(p)] == p { skip = true; break }
		}
		if !skip { out = append(out, e) }
	}
	return out
}
