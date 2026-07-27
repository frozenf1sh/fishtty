/**
 * Terminal 组件 — xterm.js 终端模拟器封装。
 *
 * 集成 WebGL 渲染加速、自适应尺寸（Fit）、
 * 50ms Resize 节流，以及物理键盘控制字符映射。
 * 实现本地回显（Local Echo）以降低远程连接延迟体感。
 */

import { useEffect, useRef, useCallback } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { matchKeyEvent } from './keymap';
import type { FishTTYClient } from '@/ws/client';
import '@xterm/xterm/css/xterm.css';

// ── Local Echo 状态 ──

/** 本地回显缓冲区：记录已在前端渲染但尚未被服务端输出覆盖的字符 */
class EchoBuffer {
  private pending = '';
  private decoder = new TextDecoder();

  /** 将用户输入写入终端并记录到待匹配队列 */
  writeLocal(term: XTerm, data: string): void {
    term.write(data);
    this.pending += data;
    // 防止内存无限增长
    if (this.pending.length > 1024) {
      this.pending = this.pending.slice(-512);
    }
  }

  /**
   * 消费服务端返回的数据，去除与本地回显重复的前缀。
   * 返回去除前缀后应写入终端的字节。
   */
  drain(serverData: Uint8Array): Uint8Array {
    if (this.pending.length === 0) return serverData;

    const serverStr = this.decoder.decode(serverData);
    let matchLen = 0;
    const maxLen = Math.min(this.pending.length, serverStr.length);

    while (matchLen < maxLen && this.pending[matchLen] === serverStr[matchLen]) {
      matchLen++;
    }

    if (matchLen > 0) {
      this.pending = this.pending.slice(matchLen);
    } else {
      // 不匹配（如 Tab 补全、Ctrl-C 等）：清空待匹配区
      this.pending = '';
    }

    if (matchLen >= serverStr.length) {
      return new Uint8Array(0);
    }
    return serverData.slice(matchLen);
  }

  /** 清空待匹配区（用于 Ctrl-C、Enter 等不可预测场景） */
  clear(): void {
    this.pending = '';
  }
}

// ── 对外暴露的句柄 ──

export interface TerminalHandle {
  term: XTerm;
  /** 消费服务端数据，去除本地回显重复前缀 */
  drainEcho: (data: Uint8Array) => Uint8Array;
}

// ── Props ──

interface TerminalProps {
  /** 所属 session ID */
  sessionId: string;
  /** WebSocket 客户端（用于发送数据） */
  client: FishTTYClient;
  /** 是否可见（不可见时不渲染以节省资源） */
  visible: boolean;
  /** xterm 实例 + echo drainer 就绪时的回调 */
  onTermReady?: (handle: TerminalHandle) => void;
}

// ── 终端主题 ──

const TERMINAL_THEME = {
  background: '#1e1e1e',
  foreground: '#d4d4d4',
  cursor: '#ffffff',
  cursorAccent: '#1e1e1e',
  selectionBackground: '#264f78',
  selectionForeground: '#ffffff',
  black: '#000000',
  red: '#cd3131',
  green: '#0dbc79',
  yellow: '#e5e510',
  blue: '#2472c8',
  magenta: '#bc3fbc',
  cyan: '#11a8cd',
  white: '#e5e5e5',
  brightBlack: '#666666',
  brightRed: '#f14c4c',
  brightGreen: '#23d18b',
  brightYellow: '#f5f543',
  brightBlue: '#3b8eea',
  brightMagenta: '#d670d6',
  brightCyan: '#29b8db',
  brightWhite: '#ffffff',
} as const;

// ── 组件 ──

export default function TerminalView({ sessionId, client, visible, onTermReady }: TerminalProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerm | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const resizeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const columnsRef = useRef(80);
  const rowsRef = useRef(24);
  const echoRef = useRef<EchoBuffer>(new EchoBuffer());

  // ── 初始化 xterm.js ──
  useEffect(() => {
    if (!containerRef.current) return;

    const term = new XTerm({
      theme: TERMINAL_THEME,
      fontSize: 13,
      fontFamily: "'Menlo', 'Monaco', 'Courier New', 'Cascadia Code', monospace",
      letterSpacing: 0,
      lineHeight: 1.2,
      cursorBlink: true,
      cursorStyle: 'bar',
      allowProposedApi: true,
      scrollback: 10000,
      cols: 80,
      rows: 24,
      smoothScrollDuration: 0,
      drawBoldTextInBrightColors: false,
      fastScrollSensitivity: 5,
      minimumContrastRatio: 1,
      wordSeparator: ' ()[]{}\'"`',
    });

    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    fitAddonRef.current = fitAddon;

    term.open(containerRef.current);
    termRef.current = term;

    // 通知父组件 xterm 已就绪
    if (onTermReady) {
      onTermReady({
        term,
        drainEcho: (data: Uint8Array) => echoRef.current.drain(data),
      });
    }

    setTimeout(() => {
      fitAddon.fit();
      columnsRef.current = term.cols;
      rowsRef.current = term.rows;
      sendResize();
    }, 50);

    return () => {
      term.dispose();
      termRef.current = null;
      fitAddonRef.current = null;
    };
  }, [sessionId]);

  // ── Resize 处理（50ms 节流） ──
  const sendResize = useCallback(() => {
    if (resizeTimerRef.current) {
      clearTimeout(resizeTimerRef.current);
    }
    resizeTimerRef.current = setTimeout(() => {
      const term = termRef.current;
      if (!term) return;
      const cols = term.cols;
      const rows = term.rows;
      if (cols !== columnsRef.current || rows !== rowsRef.current) {
        columnsRef.current = cols;
        rowsRef.current = rows;
        client.sendResize(sessionId, cols, rows);
      }
    }, 50);
  }, [client, sessionId]);

  // 监听窗口 resize / 旋转 / 键盘弹起
  useEffect(() => {
    const handleResize = () => {
      fitAddonRef.current?.fit();
      sendResize();
    };

    const handleOrientation = () => {
      setTimeout(handleResize, 100);
    };

    window.addEventListener('resize', handleResize);
    window.addEventListener('orientationchange', handleOrientation);

    const container = containerRef.current;
    let observer: ResizeObserver | null = null;
    if (container) {
      observer = new ResizeObserver(() => {
        fitAddonRef.current?.fit();
        sendResize();
      });
      observer.observe(container);
    }

    return () => {
      window.removeEventListener('resize', handleResize);
      window.removeEventListener('orientationchange', handleOrientation);
      observer?.disconnect();
    };
  }, [sendResize]);

  // ── 物理键盘映射 ──
  useEffect(() => {
    const term = termRef.current;
    if (!term) return;

    const echo = echoRef.current;

    const handleKey = (e: KeyboardEvent) => {
      if (
        e.target instanceof HTMLInputElement ||
        e.target instanceof HTMLTextAreaElement
      ) {
        return;
      }

      const mapping = matchKeyEvent(e);
      if (mapping) {
        e.preventDefault();
        // 控制键清空 echo 缓冲区
        echo.clear();
        client.sendData(sessionId, mapping.bytes);
      }
    };

    // xterm.js 内部的按键事件（处理普通字符输入）
    // 实现本地回显：立刻在终端显示，同时发送到服务器
    const handleTermData = (data: string) => {
      const encoder = new TextEncoder();
      echo.writeLocal(term, data);
      client.sendData(sessionId, encoder.encode(data));
    };

    term.onData(handleTermData);
    document.addEventListener('keydown', handleKey);

    return () => {
      document.removeEventListener('keydown', handleKey);
    };
  }, [client, sessionId]);

  // ── 渲染 ──
  return (
    <div
      ref={containerRef}
      className="terminal-container"
      style={{
        width: '100%',
        height: '100%',
        display: visible ? 'block' : 'none',
        overflow: 'hidden',
      }}
    />
  );
}

// ── 导出工具方法 ──

/** 向 xterm.js 实例写入终端输出 */
export function writeToTerminal(term: XTerm | null, data: Uint8Array): void {
  if (!term) return;
  term.write(data);
}
