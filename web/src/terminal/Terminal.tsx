/**
 * Terminal 组件 — xterm.js 终端模拟器封装。
 *
 * 集成 WebGL 渲染加速（Canvas 自动回退）、Unicode11 列宽修正、
 * 自适应尺寸（Fit）、50ms Resize 节流、物理键盘控制字符映射，
 * 交替缓冲区感知的本地回显，以及 rAF 批量发送优化。
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

// ── 本地回显状态 ──

/**
 * 交替缓冲区感知的本地回显缓冲区。
 *
 * 在正常 shell 模式下：逐字符前缀匹配，去除服务端回显与本地显示的重叠。
 * 在交替缓冲区模式下（vim/less 等全屏 TUI）：直接透传服务端数据，
 * 不进行本地写入或前缀匹配，因为 TUI 的响应不是简单字符回显。
 */
class EchoBuffer {
  private pending = '';
  private decoder = new TextDecoder();
  /** 是否处于交替缓冲区模式（vim、less 等全屏 TUI） */
  inAltBuffer = false;

  /** 将用户输入写入终端并记录到待匹配队列（仅在正常模式下） */
  writeLocal(term: XTerm, data: string): void {
    if (this.inAltBuffer) {
      // 交替缓冲区内：不进行本地回显，信任服务端渲染
      return;
    }
    term.write(data);
    this.pending += data;
    // 防止内存无限增长：截断到 1024 字节
    if (this.pending.length > 1024) {
      this.pending = this.pending.slice(-512);
    }
  }

  /**
   * 消费服务端返回的数据。
   * - 正常模式：去除与本地回显重复的前缀，返回剩余数据。
   * - 交替缓冲区模式：直接透传全部数据。
   */
  drain(serverData: Uint8Array): Uint8Array {
    // 交替缓冲区模式：透传，跳过前缀匹配
    if (this.inAltBuffer) {
      return serverData;
    }

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
  const webglAddonRef = useRef<WebglAddon | null>(null);
  const resizeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const columnsRef = useRef(80);
  const rowsRef = useRef(24);
  const echoRef = useRef<EchoBuffer>(new EchoBuffer());
  /** rAF 批量发送：累积的输入数据 */
  const pendingInputRef = useRef<Uint8Array[]>([]);
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

    // 1. FitAddon — 自适应尺寸（必须最先加载）
    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    fitAddonRef.current = fitAddon;

    // 2. WebGL 渲染器 — GPU 加速（主渲染器）
    try {
      const webglAddon = new WebglAddon();
      term.loadAddon(webglAddon);
      webglAddonRef.current = webglAddon;

      // WebGL context 丢失时降级到 Canvas
      webglAddon.onContextLoss(() => {
        console.warn('[fishtty] WebGL context 丢失，降级到 Canvas 渲染器');
        webglAddon.dispose();
        webglAddonRef.current = null;
        try {
          const canvasAddon = new CanvasAddon();
          term.loadAddon(canvasAddon);
        } catch {
          console.warn('[fishtty] Canvas 渲染器不可用，使用默认 DOM 渲染器');
        }
      });
    } catch {
      // WebGL 不可用，回退到 Canvas
      console.warn('[fishtty] WebGL 不可用，使用 Canvas 渲染器');
      try {
        term.loadAddon(new CanvasAddon());
      } catch {
        console.warn('[fishtty] Canvas 渲染器不可用，使用默认 DOM 渲染器');
      }
    }

    // 3. Unicode11 Addon — 修正 CJK/emoji 等宽字符的列宽计算
    term.loadAddon(new Unicode11Addon());
    term.unicode.activeVersion = '11';

    // 4. 交替缓冲区检测：通过 CSI handler 在解析阶段拦截
    const echo = echoRef.current;
    try {
      term.parser.registerCsiHandler({ prefix: '?', final: 'h' }, (params) => {
        // params[0] 可能是 number | number[]，取第一个值
        const code = Array.isArray(params[0]) ? params[0][0] : params[0];
        // 1049: 保存光标 + 进入交替缓冲区
        // 1047: 仅进入交替缓冲区
        if (code === 1049 || code === 1047) {
          echo.inAltBuffer = true;
          echo.clear();
          console.debug('[fishtty] 进入交替缓冲区 (CSI ?%dh)', code);
        }
        return false; // 不拦截，让 xterm.js 继续处理
      });

      term.parser.registerCsiHandler({ prefix: '?', final: 'l' }, (params) => {
        const code = Array.isArray(params[0]) ? params[0][0] : params[0];
        if (code === 1049 || code === 1047) {
          echo.inAltBuffer = false;
          console.debug('[fishtty] 退出交替缓冲区 (CSI ?%dl)', code);
        }
        return false;
      });
    } catch {
      // registerCsiHandler 在某些 xterm 版本可能不可用，降级为无检测
      console.warn('[fishtty] registerCsiHandler 不可用，交替缓冲区检测关闭');
    }

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
      webglAddonRef.current = null;
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

  // ── rAF 批量发送 ──
  /**
   * 刷新累积的输入：合并所有 pending Uint8Array → 单次 protobuf 序列化 → 发送。
   */
  const flushPendingInput = useCallback(() => {
    const pending = pendingInputRef.current;
    if (pending.length === 0) {
      rafScheduledRef.current = false;
      return;
    }

    // 计算总字节数
    const totalLen = pending.reduce((sum, arr) => sum + arr.length, 0);
    const merged = new Uint8Array(totalLen);
    let offset = 0;
    for (const arr of pending) {
      merged.set(arr, offset);
      offset += arr.length;
    }

    client.sendData(sessionId, merged);
    pendingInputRef.current = [];
    rafScheduledRef.current = false;
  }, [client, sessionId]);

  // ── 物理键盘映射 ──
  useEffect(() => {
    const term = termRef.current;
    if (!term) return;

    const echo = echoRef.current;

    // keydown 事件：处理控制字符和特殊键
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
        // 控制键立即发送，不经过 rAF 合并
        client.sendData(sessionId, mapping.bytes);
      }
    };

    // term.onData：处理普通字符输入
    // 本地回显立即渲染，发送端通过 rAF 合并以减少序列化开销
    const handleTermData = (data: string) => {
      const encoder = encoderRef.current;
      const bytes = encoder.encode(data);

      // 本地回显：立即写入终端（用户感知零延迟）
      echo.writeLocal(term, data);

      // 发送端：rAF 合并
      pendingInputRef.current.push(bytes);
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
