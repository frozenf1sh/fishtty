/**
 * Terminal 组件 — xterm.js 终端模拟器封装。
 *
 * 集成 WebGL 渲染加速、自适应尺寸（Fit）、
 * 50ms Resize 节流，以及物理键盘控制字符映射。
 */

import { useEffect, useRef, useCallback } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { matchKeyEvent } from './keymap';
import type { FishTTYClient } from '@/ws/client';
import '@xterm/xterm/css/xterm.css';

// ── Props ──

interface TerminalProps {
  /** 所属 session ID */
  sessionId: string;
  /** WebSocket 客户端（用于发送数据） */
  client: FishTTYClient;
  /** 是否可见（不可见时不渲染以节省资源） */
  visible: boolean;
  /** xterm 实例就绪时的回调，用于外部写入终端输出 */
  onTermReady?: (term: XTerm) => void;
}

// ── 终端主题 ──

const TERMINAL_THEME = {
  background: '#1e1e1e',
  foreground: '#d4d4d4',
  cursor: '#ffffff',
  cursorAccent: '#1e1e1e',
  selectionBackground: '#264f78',
  selectionForeground: '#ffffff',
  // 16 色 ANSI 调色板
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
      // 不禁用 stdin — onData 回调负责将所有输入通过 WebSocket 发送到 PTY
      scrollback: 10000,
      cols: 80,
      rows: 24,
      // 改善复杂 prompt/补全插件渲染
      smoothScrollDuration: 0,          // 禁用平滑滚动，避免滚动残影
      drawBoldTextInBrightColors: false, // bold 文本不变色，避免 prompt 颜色错乱
      fastScrollSensitivity: 5,
      minimumContrastRatio: 1,
      wordSeparator: ' ()[]{}\'"`',
    });

    // 使用 DOM 渲染器（兼容性最好，WebGL 在部分环境有渲染问题）

    // Fit 自适应尺寸
    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    fitAddonRef.current = fitAddon;

    term.open(containerRef.current);
    termRef.current = term;

    // 通知父组件 xterm 已就绪
    if (onTermReady) onTermReady(term);

    // 打开后立即 fit
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
      // 旋转后延迟 fit（等布局完成）
      setTimeout(handleResize, 100);
    };

    window.addEventListener('resize', handleResize);
    window.addEventListener('orientationchange', handleOrientation);

    // 使用 ResizeObserver 监听容器变化（键盘弹起/收起）
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

    const handleKey = (e: KeyboardEvent) => {
      // 不拦截输入框中的按键
      if (
        e.target instanceof HTMLInputElement ||
        e.target instanceof HTMLTextAreaElement
      ) {
        return;
      }

      const mapping = matchKeyEvent(e);
      if (mapping) {
        e.preventDefault();
        client.sendData(sessionId, mapping.bytes);
      }
    };

    // xterm.js 内部的按键事件（处理普通字符输入）
    const handleTermData = (data: string) => {
      const encoder = new TextEncoder();
      client.sendData(sessionId, encoder.encode(data));
    };

    term.onData(handleTermData);
    document.addEventListener('keydown', handleKey);

    return () => {
      document.removeEventListener('keydown', handleKey);
    };
  }, [client, sessionId]);

  // ── 暴露 write 方法给外部 ──
  // 通过自定义事件或 ref，让外部能写入数据到终端
  // 这里使用 window 自定义事件

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

// ── 导出工具方法：写入数据到终端 ──

/** 向 xterm.js 实例写入终端输出 */
export function writeToTerminal(term: XTerm | null, data: Uint8Array): void {
  if (!term) return;
  term.write(data);
}
