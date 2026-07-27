/**
 * Terminal 组件 — xterm.js 终端模拟器封装。
 *
 * 集成 WebGL 渲染加速（Canvas 自动回退）、Unicode11 列宽修正、
 * 自适应尺寸（Fit）、50ms Resize 节流、物理键盘控制字符映射，
 * 交替缓冲区感知的本地回显（序号追踪），以及 rAF 批量发送优化。
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

// ── 本地回显状态：序号追踪 ──

/**
 * 基于本地序号的 EchoBuffer。
 *
 * 用单调递增的 echoSeq 追踪每次本地写入，替代逐字符前缀匹配。
 * 服务端回显 DataChunk 携带 echo_seq，drain 时按序号查找对应的
 * 本地写入并剥离前缀。对任意 RTT 均正确，不依赖时序假设。
 *
 * 交替缓冲区模式下暂停序号追踪，直接透传服务端数据。
 */
class EchoBuffer {
  /** 待确认的本地写入：echoSeq → 写入的字符串 */
  private pending = new Map<number, string>();
  private decoder = new TextDecoder();
  /** 当前本地序号（单调递增，uint32 回绕） */
  private echoSeq = 0;
  /** 是否处于交替缓冲区模式（vim、less 等全屏 TUI） */
  inAltBuffer = false;

  /** 递增序号，返回新序号（带回绕保护） */
  nextSeq(): number {
    this.echoSeq++;
    // uint32 溢出回绕：归零并清空 pending
    if (this.echoSeq > 0xFFFFFFFF) {
      this.echoSeq = 1;
      this.pending.clear();
    }
    return this.echoSeq;
  }

  /**
   * 将用户输入写入终端并记录序号。
   * 交替缓冲区模式下跳过本地写入。
   * @returns 分配的 echoSeq，用于发送时携带
   */
  writeLocal(term: XTerm, data: string): number {
    if (this.inAltBuffer) {
      return 0; // 交替缓冲区：不追踪
    }
    const seq = this.nextSeq();
    term.write(data);
    this.pending.set(seq, data);
    return seq;
  }

  /**
   * 消费服务端返回的数据。
   * - 正常模式：按 echoSeq 查找 pending，匹配时剥离前缀并删除条目。
   * - 交替缓冲区模式：直接透传全部数据。
   * @param serverData 服务端返回的原始数据
   * @param echoSeq 服务端携带的 echo_seq（0 = 未追踪）
   */
  drain(serverData: Uint8Array, echoSeq: number = 0): Uint8Array {
    // 交替缓冲区模式：透传
    if (this.inAltBuffer) {
      return serverData;
    }

    // echoSeq 为 0 表示未启用追踪，回退到简单行为
    if (echoSeq === 0 || this.pending.size === 0) {
      // 无 pending 时直接透传
      if (this.pending.size === 0) return serverData;
      // 有 pending 但 echoSeq=0：清空并透传（向后兼容旧 agent）
      this.pending.clear();
      return serverData;
    }

    const local = this.pending.get(echoSeq);
    if (local === undefined) {
      // 序号未命中（可能是旧序号或已清理）：透传
      return serverData;
    }

    // 序号命中：尝试前缀匹配，剥离已本地显示的字符
    this.pending.delete(echoSeq);
    const serverStr = this.decoder.decode(serverData);

    // 逐字符比较本地写入与服务端回显
    let matchLen = 0;
    const maxLen = Math.min(local.length, serverStr.length);
    while (matchLen < maxLen && local[matchLen] === serverStr[matchLen]) {
      matchLen++;
    }

    if (matchLen >= serverStr.length) {
      return new Uint8Array(0); // 完全匹配，不写入
    }
    return serverData.slice(matchLen); // 部分匹配，写入不匹配的尾部
  }

  /** 清空所有待确认条目（用于 Ctrl-C、Enter 等不可预测场景） */
  clear(): void {
    this.pending.clear();
  }
}

// ── 对外暴露的句柄 ──

export interface TerminalHandle {
  term: XTerm;
  /** 消费服务端数据，去除本地回显重复前缀。echoSeq 为服务端携带的 echo_seq。 */
  drainEcho: (data: Uint8Array, echoSeq?: number) => Uint8Array;
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
  const webglAddonRef = useRef<WebglAddon | null>(null);
  const resizeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const columnsRef = useRef(80);
  const rowsRef = useRef(24);
  const echoRef = useRef<EchoBuffer>(new EchoBuffer());
  /** rAF 批量发送：累积 {bytes, seq} */
  const pendingInputRef = useRef<{ bytes: Uint8Array; seq: number }[]>([]);
  const rafScheduledRef = useRef(false);
  const encoderRef = useRef(new TextEncoder());

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

    // 4. 交替缓冲区检测
    const echo = echoRef.current;
    try {
      term.parser.registerCsiHandler({ prefix: '?', final: 'h' }, (params) => {
        const code = Array.isArray(params[0]) ? params[0][0] : params[0];
        if (code === 1049 || code === 1047) {
          echo.inAltBuffer = true;
          echo.clear();
        }
        return false;
      });
      term.parser.registerCsiHandler({ prefix: '?', final: 'l' }, (params) => {
        const code = Array.isArray(params[0]) ? params[0][0] : params[0];
        if (code === 1049 || code === 1047) {
          echo.inAltBuffer = false;
        }
        return false;
      });
    } catch { /* fallback */ }

    term.open(containerRef.current);
    termRef.current = term;

    if (onTermReady) {
      onTermReady({
        term,
        drainEcho: (data: Uint8Array, echoSeq?: number) => echoRef.current.drain(data, echoSeq),
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

  // ── rAF 批量发送（携带 echo_seq） ──
  const flushPendingInput = useCallback(() => {
    const pending = pendingInputRef.current;
    if (pending.length === 0) {
      rafScheduledRef.current = false;
      return;
    }
    const totalLen = pending.reduce((sum, e) => sum + e.bytes.length, 0);
    const merged = new Uint8Array(totalLen);
    let offset = 0;
    for (const e of pending) {
      merged.set(e.bytes, offset);
      offset += e.bytes.length;
    }
    // 使用第一个字符的序号作为 batch 的 echo_seq
    const echoSeq = pending[0].seq;
    client.sendData(sessionId, merged, echoSeq);
    pendingInputRef.current = [];
    rafScheduledRef.current = false;
  }, [client, sessionId]);

  // ── 物理键盘映射 ──
  useEffect(() => {
    const term = termRef.current;
    if (!term) return;
    const echo = echoRef.current;

    // keydown: 控制字符和特殊键 —— 立即发送，不经过 rAF
    const handleKey = (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;
      const mapping = matchKeyEvent(e);
      if (mapping) {
        e.preventDefault();
        echo.clear();
        // 控制键：echo_seq=0（不追踪回显）
        client.sendData(sessionId, mapping.bytes, 0);
      }
    };

    // term.onData: 普通字符 —— 本地回显即时 + rAF 合并发送
    const handleTermData = (data: string) => {
      const bytes = encoderRef.current.encode(data);
      const seq = echo.writeLocal(term, data);
      pendingInputRef.current.push({ bytes, seq });
      if (!rafScheduledRef.current) {
        rafScheduledRef.current = true;
        requestAnimationFrame(() => flushPendingInput());
      }
    };

    term.onData(handleTermData);
    document.addEventListener('keydown', handleKey);
    return () => {
      document.removeEventListener('keydown', handleKey);
    };
  }, [client, sessionId, flushPendingInput]);

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
