/**
 * Terminal 组件 — xterm.js 终端模拟器封装。
 *
 * 集成 WebGL 渲染加速（Canvas 自动回退）、Unicode11 列宽修正、
 * 自适应尺寸（Fit）、50ms Resize 节流、物理键盘控制字符映射。
 *
 * 无本地回显：所有键盘输入直接发送到服务端，终端显示完全由 PTY 输出驱动。
 * 这避免了本地/远程回显的不同步问题，代价是按键到显示有一个 RTT 延迟。
 */

import { useEffect, useRef, useCallback } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebglAddon } from '@xterm/addon-webgl';
import { CanvasAddon } from '@xterm/addon-canvas';
import { Unicode11Addon } from '@xterm/addon-unicode11';
import { matchKeyEvent } from './keymap';
import type { FishTTYClient } from '@/ws/client';
import '@xterm/xterm/css/xterm.css';

// ── 对外暴露的句柄 ──

export interface TerminalHandle {
  term: XTerm;
}

// ── Props ──

interface TerminalProps {
  /** 所属 session ID */
  sessionId: string;
  /** WebSocket 客户端（用于发送数据） */
  client: FishTTYClient;
  /** 是否可见（不可见时不渲染以节省资源） */
  visible: boolean;
  /** xterm 实例就绪时的回调 */
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
  const webglAddonRef = useRef<WebglAddon | null>(null);
  const resizeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const columnsRef = useRef(80);
  const rowsRef = useRef(24);
  const encoder = useRef(new TextEncoder());
  /** 当前终端是否可见（用于 keydown handler 隔离多 session 按键） */
  const visibleRef = useRef(visible);
  visibleRef.current = visible;

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

    // 1. FitAddon — 自适应尺寸
    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    fitAddonRef.current = fitAddon;

    // 2. WebGL 渲染器 — GPU 加速（主渲染器）
    try {
      const webglAddon = new WebglAddon();
      term.loadAddon(webglAddon);
      webglAddonRef.current = webglAddon;
      webglAddon.onContextLoss(() => {
        console.warn('[fishtty] WebGL context 丢失，降级到 Canvas 渲染器');
        webglAddon.dispose();
        webglAddonRef.current = null;
        try { term.loadAddon(new CanvasAddon()); } catch { /* DOM fallback */ }
      });
    } catch {
      console.warn('[fishtty] WebGL 不可用，使用 Canvas 渲染器');
      try { term.loadAddon(new CanvasAddon()); } catch { /* DOM fallback */ }
    }

    // 3. Unicode11 Addon — 修正 CJK/emoji 列宽
    term.loadAddon(new Unicode11Addon());
    term.unicode.activeVersion = '11';

    term.open(containerRef.current);
    termRef.current = term;

    if (onTermReady) {
      onTermReady({ term });
    }

    setTimeout(() => {
      fitAddon.fit();
      columnsRef.current = term.cols;
      rowsRef.current = term.rows;
      sendResize();
    }, 50);

    return () => {
      if (resizeTimerRef.current) clearTimeout(resizeTimerRef.current);
      term.dispose();
      termRef.current = null;
      fitAddonRef.current = null;
      webglAddonRef.current = null;
    };
  }, [sessionId]);

  // ── Resize 处理（50ms 节流） ──
  const sendResize = useCallback(() => {
    if (resizeTimerRef.current) clearTimeout(resizeTimerRef.current);
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

  useEffect(() => {
    const handleResize = () => { fitAddonRef.current?.fit(); sendResize(); };
    const handleOrientation = () => setTimeout(handleResize, 100);
    window.addEventListener('resize', handleResize);
    window.addEventListener('orientationchange', handleOrientation);
    const container = containerRef.current;
    let observer: ResizeObserver | null = null;
    if (container) {
      observer = new ResizeObserver(() => { fitAddonRef.current?.fit(); sendResize(); });
      observer.observe(container);
    }
    return () => {
      window.removeEventListener('resize', handleResize);
      window.removeEventListener('orientationchange', handleOrientation);
      observer?.disconnect();
    };
  }, [sendResize]);

  // ── 键盘输入：纯透传，无本地回显 ──
  useEffect(() => {
    const term = termRef.current;
    if (!term) return;

    // keydown: 控制字符和特殊键
    const handleKey = (e: KeyboardEvent) => {
      if (!visibleRef.current) return;
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;
      const mapping = matchKeyEvent(e);
      if (mapping) {
        e.preventDefault();
        client.sendData(sessionId, mapping.bytes);
      }
    };

    // term.onData: 普通字符 —— 只发送，不本地显示
    // 所有显示由服务端 PTY 输出驱动，xterm 只负责渲染接收到的字节流
    const handleTermData = (data: string) => {
      client.sendData(sessionId, encoder.current.encode(data));
    };

    term.onData(handleTermData);
    document.addEventListener('keydown', handleKey);
    return () => {
      document.removeEventListener('keydown', handleKey);
    };
  }, [client, sessionId]);

  return (
    <div
      ref={containerRef}
      className="terminal-container"
      style={{ width: '100%', height: '100%', display: visible ? 'block' : 'none', overflow: 'hidden' }}
    />
  );
}

export function writeToTerminal(term: XTerm | null, data: Uint8Array): void {
  if (!term) return;
  term.write(data);
}
