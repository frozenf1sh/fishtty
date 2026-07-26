package agent

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// PtySession 封装一个 pseudo-terminal 及其运行的命令。
// 通过 creack/pty 管理 PTY master fd 与子进程。
type PtySession struct {
	mu sync.Mutex

	f      *os.File    // PTY master 文件描述符
	cmd    *exec.Cmd   // 运行的命令（shell 等）
	closed bool        // 是否已关闭
	rows   uint32      // 当前终端行数
	cols   uint32      // 当前终端列数
}

// PtyConfig 定义 PTY 会话的创建参数。
type PtyConfig struct {
	Command string            // 要执行的命令；空字符串表示默认 shell
	Cols    uint32            // 初始列数
	Rows    uint32            // 初始行数
	Env     map[string]string // 额外环境变量
	WorkDir string            // 工作目录；空字符串表示用户主目录
}

// NewPty 创建新的 PTY 会话，启动指定的命令。
// 如果 config.Command 为空，则启动用户默认 shell。
func NewPty(config PtyConfig) (*PtySession, error) {
	if config.Cols == 0 {
		config.Cols = 80
	}
	if config.Rows == 0 {
		config.Rows = 24
	}

	cmd := exec.Command(config.Command)
	if config.Command == "" {
		// 使用默认 shell
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		cmd = exec.Command(shell)
	}

	// 设置环境变量：继承 Agent 环境但过滤 Claude/IDE 标记，避免污染 PTY 子进程
	cmd.Env = filterEnv(os.Environ())
	for k, v := range config.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	// PTY 终端环境确保 — 修复补全/美化插件导致的字符错乱
	cmd.Env = append(cmd.Env,
		"TERM=xterm-256color",        // xterm.js 完整兼容
		"COLORTERM=truecolor",         // 24-bit 颜色，避免插件降级渲染
		"LANG=en_US.UTF-8",            // 正确 locale，防止编码错乱
		"LC_ALL=en_US.UTF-8",
		"TERM_PROGRAM=fishtty",        // 让 shell 配置能检测并简化 prompt
	)

	// 设置工作目录
	if config.WorkDir != "" {
		cmd.Dir = config.WorkDir
	}

	// 使用指定尺寸启动 PTY
	winSize := &pty.Winsize{
		Rows: uint16(config.Rows),
		Cols: uint16(config.Cols),
		X:    uint16(config.Cols * 8),
		Y:    uint16(config.Rows * 16),
	}

	f, err := pty.StartWithSize(cmd, winSize)
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}

	return &PtySession{
		f:    f,
		cmd:  cmd,
		rows: config.Rows,
		cols: config.Cols,
	}, nil
}

// Read 从 PTY master 读取终端输出。
// 这是阻塞调用；当 PTY 关闭或出错时返回 error。
func (p *PtySession) Read(buf []byte) (int, error) {
	return p.f.Read(buf)
}

// Write 向 PTY master 写入数据（stdin 到终端）。
func (p *PtySession) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.ErrClosedPipe
	}
	return p.f.Write(data)
}

// Resize 调整 PTY 窗口大小，触发 SIGWINCH 给前台进程组。
func (p *PtySession) Resize(cols, rows uint32) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return io.ErrClosedPipe
	}

	winSize := &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
		X:    uint16(cols * 8),
		Y:    uint16(rows * 16),
	}

	if err := pty.Setsize(p.f, winSize); err != nil {
		return fmt.Errorf("pty setsize: %w", err)
	}

	p.rows = rows
	p.cols = cols
	return nil
}

// Size 返回当前 PTY 尺寸（cols, rows）。
func (p *PtySession) Size() (uint32, uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cols, p.rows
}

// Pid 返回 PTY 子进程的 PID。
func (p *PtySession) Pid() int {
	if p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

// Close 关闭 PTY master fd，并等待子进程退出。
// 首先发送 SIGHUP，等待 2 秒后若进程未退出则 SIGKILL。
func (p *PtySession) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true

	var closeErr error

	// 关闭 PTY master fd（这会向子进程发送 SIGHUP）
	if err := p.f.Close(); err != nil {
		closeErr = err
	}

	// 等待进程退出或强制 kill
	if p.cmd.Process != nil {
		// creack/pty 的 Start 已设置 cmd.SysProcAttr，Wait 会正确处理
		_ = p.cmd.Wait()
	}

	return closeErr
}

// ForceKill 强制终止子进程（SIGKILL）。
func (p *PtySession) ForceKill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// ── 环境变量过滤 ──

// claudeEnvPrefixes 是需要从 PTY 环境中过滤掉的前缀。
// 这些变量来自 Claude Code / IDE 运行时，不应泄漏到终端会话中。
var claudeEnvPrefixes = []string{
	"CLAUDE_",
	"ANTHROPIC_",
	"COPILOT_",
	"VSCODE_",
	"CODE_",
	"JETBRAINS_",
	"IDEA_",
}

// filterEnv 过滤掉不应泄漏到 PTY 的环境变量。
func filterEnv(environ []string) []string {
	var filtered []string
	for _, e := range environ {
		skip := false
		for _, prefix := range claudeEnvPrefixes {
			if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
				skip = true
				break
			}
		}
		if !skip {
			filtered = append(filtered, e)
		}
	}
	return filtered
}
