//go:build linux

package pty

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// setRawMinimal 在 Linux 上设置最小 raw 模式：仅禁用内核回显 (ECHO) 和行缓冲 (ICANON)，
// 保留 OPOST/ONLCR（输出 \n→\r\n 转换）、ISIG（信号生成）、IEXTEN 等。
// 与 term.MakeRaw 的关键区别：保留 Oflag 中的 OPOST 和 ONLCR，避免换行错乱。
func setRawMinimal(fd int) error {
	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return fmt.Errorf("tcgetattr: %w", err)
	}

	// 仅关闭回显和行缓冲
	termios.Lflag &^= unix.ECHO | unix.ECHOE | unix.ECHOK | unix.ICANON

	// 非规范模式：有数据立即返回
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0

	return unix.IoctlSetTermios(fd, unix.TCSETS, termios)
}
