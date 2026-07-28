/**
 * fishtty PWA — App 根组件。
 *
 * 包含：设备列表页 → 终端会话页，
 * Session 选项卡切换、断线重连遮罩、分级错误 Toast。
 * 集成移动端键盘适配、本地回显优化、自动会话恢复。
 */

import { useState, useRef, useCallback, useEffect } from 'react';
import type { TunnelMessage } from '@/gen/fishtty/v1/tunnel_pb';
import { ErrorCode } from '@/gen/fishtty/v1/tunnel_pb';
import { FishTTYClient, type WsState } from '@/ws/client';
import { SessionProvider, useSession } from '@/sessions/SessionProvider';
import TerminalView from '@/terminal/Terminal';
import type { TerminalHandle } from '@/terminal/Terminal';
import VirtualKeyboard from '@/terminal/VirtualKeyboard';

// ── 常量 ──

/** 默认 Server 地址：始终与 PWA 加载来源一致 */
const DEFAULT_SERVER = (() => {
  try { return window.location.origin; } catch { return 'http://localhost:8080'; }
})();
const LS_SERVER_KEY = 'fishtty_server';
const LS_DEVICE_ID_KEY = 'fishtty_device_id';

// ── Toast 类型 ──

type ToastLevel = 'error' | 'warning' | 'info';

interface Toast {
  id: number;
  message: string;
  level: ToastLevel;
}

let toastIdCounter = 0;

// ── App 入口 ──

export default function App() {
  return (
    <SessionProvider>
      <AppShell />
    </SessionProvider>
  );
}

// ── App 壳 ──

function AppShell() {
  const [view, setView] = useState<'devices' | 'terminal'>('devices');
  const [serverUrl, setServerUrl] = useState(() =>
    localStorage.getItem(LS_SERVER_KEY) || DEFAULT_SERVER
  );
  const [deviceId, setDeviceId] = useState(() =>
    localStorage.getItem(LS_DEVICE_ID_KEY) || FishTTYClient.getPersistedDeviceId() || ''
  );
  const [wsState, setWsState] = useState<WsState>('WS_DISCONNECTED');
  const [toasts, setToasts] = useState<Toast[]>([]);
  const clientRef = useRef<FishTTYClient | null>(null);
  const handleRefs = useRef<Map<string, TerminalHandle>>(new Map());
  const { sessions, activeSessionId, createSession, removeSession, switchSession } = useSession();

  // ── 移动端键盘适配 ──
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;

    const updateKeyboard = () => {
      const keyboardH = window.innerHeight - vv.height;
      document.documentElement.style.setProperty(
        '--keyboard-height', `${Math.max(0, keyboardH)}px`
      );
    };

    vv.addEventListener('resize', updateKeyboard);
    vv.addEventListener('scroll', updateKeyboard);
    return () => {
      vv.removeEventListener('resize', updateKeyboard);
      vv.removeEventListener('scroll', updateKeyboard);
    };
  }, []);

  // ── 连接 ──
  const connect = useCallback(
    (targetDeviceId: string) => {
      if (!targetDeviceId) return;

      localStorage.setItem(LS_SERVER_KEY, serverUrl);
      localStorage.setItem(LS_DEVICE_ID_KEY, targetDeviceId);

      const client = new FishTTYClient(serverUrl, targetDeviceId, {
        onMessage(msg: TunnelMessage) {
          handleServerMessage(msg);
        },
        onStateChange(state: WsState) {
          setWsState(state);
          // WS 激活且无活跃 session 时自动创建
          if (state === 'WS_ACTIVE') {
            handleWsActive();
          }
        },
        onError(err: Error) {
          addToast(err.message, 'error');
        },
      });

      clientRef.current = client;
      client.connect();
      setDeviceId(targetDeviceId);
    },
    [serverUrl]
  );

  // ── WS 激活后的自动恢复 ──
  const handleWsActive = useCallback(() => {
    const client = clientRef.current;
    if (!client) return;

    // 若已有活跃 session，不需要自动创建
    if (client.hasActiveSessions()) return;

    // 检查是否有持久化的历史记录（说明之前使用过）
    if (FishTTYClient.hasPersistedConnection()) {
      addToast('检测到历史连接，正在自动创建终端...', 'info');
      // 延迟一小段时间确保连接稳定
      setTimeout(() => {
        autoCreateSession();
      }, 500);
    }
  }, []);

  // ── 自动创建 Session ──
  const autoCreateSession = useCallback(() => {
    const client = clientRef.current;
    if (!client || client.getState() !== 'WS_ACTIVE') return;

    const device = client.getDeviceId();
    const sid = createSession(device);
    const cols = 80;
    const rows = 24;
    const sent = client.createSession(sid, cols, rows);
    if (!sent) {
      addToast('自动创建终端失败，请手动点击 + 终端', 'warning');
    }
  }, [createSession]);

  // ── 处理 Server 消息 ──
  const handleServerMessage = useCallback(
    (msg: TunnelMessage) => {
      const sid = msg.sessionId;

      switch (msg.payload.case) {
        case 'dataChunk': {
          const chunk = msg.payload.value;
          if (!chunk.data) break;

          const handle = handleRefs.current.get(sid);
          if (handle) {
            // 无本地回显模式：服务端数据直接写入，xterm 显示完全由 PTY 输出驱动
            handle.term.write(chunk.data);
          }
          break;
        }

        case 'sessionCreated': {
          const created = msg.payload.value;
          if (created.status !== 1) {
            addToast(`会话创建失败: ${created.message || '未知错误'}`, 'error');
          }
          break;
        }

        case 'reattachData': {
          const reattach = msg.payload.value;
          const handle = handleRefs.current.get(sid);
          if (handle && reattach.chunks) {
            reattach.chunks.forEach((chunk) => {
              if (chunk.data) handle.term.write(chunk.data);
            });
          }
          break;
        }

        case 'errorMsg': {
          const err = msg.payload.value;
          handleErrorMsg(err.code, err.message);
          break;
        }

        default:
          break;
      }
    },
    []
  );

  // ── 分级错误处理 ──
  const handleErrorMsg = useCallback(
    (code: ErrorCode, message: string) => {
      switch (code) {
        case ErrorCode.SESSION_LOST:
          // Session 已过期，自动创建新终端
          addToast('终端会话已过期，正在自动创建新会话...', 'warning');
          setTimeout(() => autoCreateSession(), 300);
          break;

        case ErrorCode.SESSION_NOT_FOUND:
          addToast(`会话不存在: ${message}`, 'warning');
          break;

        case ErrorCode.AGENT_UNREACHABLE:
          addToast('目标设备不在线，请确认 Agent 已启动', 'error');
          break;

        case ErrorCode.CHANNEL_FULL:
          addToast('数据通道拥塞，部分输出已丢弃', 'warning');
          break;

        case ErrorCode.CONNECTION_TIMEOUT:
          addToast(`连接超时: ${message}`, 'error');
          break;

        case ErrorCode.UNAUTHORIZED:
          addToast('认证失败，请检查 device_id 是否正确', 'error');
          break;

        case ErrorCode.INTERNAL_ERROR:
          addToast(`服务端内部错误: ${message}`, 'error');
          break;

        case ErrorCode.COMMAND_FAILED:
          addToast(`命令执行失败: ${message}`, 'error');
          break;

        default:
          addToast(`[${ErrorCode[code] || code}] ${message}`, 'error');
      }
    },
    [autoCreateSession]
  );

  // ── WS 连上时自动切到终端页 ──
  useEffect(() => {
    if (wsState === 'WS_ACTIVE' && view === 'devices') {
      setView('terminal');
    }
  }, [wsState, view]);

  // ── Toast 管理 ──
  const addToast = useCallback((message: string, level: ToastLevel = 'error') => {
    const id = ++toastIdCounter;
    setToasts((prev) => {
      // 保留最多 3 条
      const next = [...prev, { id, message, level }];
      return next.slice(-3);
    });
    // 5 秒后自动移除
    setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== id));
    }, 5000);
  }, []);

  // ── 创建终端会话 ──
  const handleCreateSession = useCallback(() => {
    if (!clientRef.current || clientRef.current.getState() !== 'WS_ACTIVE') return;
    if (!deviceId) return;

    const sid = createSession(deviceId);
    const cols = 80;
    const rows = 24;
    const sent = clientRef.current.createSession(sid, cols, rows);
    if (!sent) {
      addToast('发送 SessionInit 失败，请刷新页面重试', 'error');
    }
  }, [deviceId, createSession, addToast]);

  // ── 销毁终端会话 ──
  const handleDestroySession = useCallback(
    (sid: string) => {
      if (clientRef.current) {
        clientRef.current.destroySession(sid);
      }
      handleRefs.current.delete(sid);
      removeSession(sid);
    },
    [removeSession]
  );

  // ── 断开 ──
  const handleDisconnect = useCallback(() => {
    clientRef.current?.disconnect();
    clientRef.current = null;
    setView('devices');
    setWsState('WS_DISCONNECTED');
  }, []);

  // ── 渲染：设备列表页 ──
  if (view === 'devices') {
    return (
      <div className="app devices-view">
        <header className="app-header">
          <h1>fishtty</h1>
          <span className="subtitle">远程终端控制</span>
        </header>

        <div className="connect-form">
          <label>
            Server 地址
            <input
              type="text"
              value={serverUrl}
              onChange={(e) => setServerUrl(e.target.value)}
              placeholder="https://your-server:8443"
            />
          </label>
          <label>
            设备 ID
            <input
              type="text"
              value={deviceId}
              onChange={(e) => setDeviceId(e.target.value)}
              placeholder="输入设备标识（如 my-pc）"
            />
          </label>
          <button
            className="btn-connect"
            onClick={() => connect(deviceId)}
            disabled={!deviceId || wsState === 'WS_CONNECTING'}
          >
            {wsState === 'WS_CONNECTING' ? '连接中...' : '连接设备'}
          </button>
          {wsState === 'WS_RECONNECTING' && (
            <p className="status-text">正在重连...</p>
          )}
        </div>

        <ToastContainer toasts={toasts} />
      </div>
    );
  }

  // ── 渲染：终端页 ──
  return (
    <div className="app terminal-view">
      {/* 顶部栏：设备信息 + 断开按钮 */}
      <header className="terminal-header">
        <button className="btn-back" onClick={handleDisconnect}>
          ← 断开
        </button>
        <span className="device-label">{deviceId}</span>
        <button className="btn-new-session" onClick={handleCreateSession}>
          + 终端
        </button>
      </header>

      {/* Session 选项卡 */}
      {sessions.length > 0 && (
        <div className="session-tabs">
          {sessions.map((s) => (
            <div
              key={s.sessionId}
              className={`session-tab ${s.sessionId === activeSessionId ? 'session-tab--active' : ''}`}
              onClick={() => switchSession(s.sessionId)}
            >
              <span className="tab-label">#{s.sessionId.slice(-4)}</span>
              <button
                className="tab-close"
                onClick={(e) => {
                  e.stopPropagation();
                  handleDestroySession(s.sessionId);
                }}
                aria-label="关闭会话"
              >
                ×
              </button>
            </div>
          ))}
        </div>
      )}

      {/* 终端区域 */}
      <div className="terminal-area">
        {sessions.map((s) => (
          <TerminalView
            key={s.sessionId}
            sessionId={s.sessionId}
            client={clientRef.current!}
            visible={s.sessionId === activeSessionId}
            onTermReady={(handle) => {
              handleRefs.current.set(s.sessionId, handle);
            }}
          />
        ))}
      </div>

      {/* 虚拟键盘 */}
      {activeSessionId && clientRef.current && (
        <VirtualKeyboard
          sessionId={activeSessionId}
          client={clientRef.current}
        />
      )}

      {/* 重连遮罩 */}
      {(wsState === 'WS_RECONNECTING' || wsState === 'WS_CONNECTING') && (
        <div className="reconnect-overlay">
          <div className="reconnect-spinner" />
          <p>重新连接中...</p>
        </div>
      )}

      {/* 分级 Toast */}
      <ToastContainer toasts={toasts} />
    </div>
  );
}

// ── 分级 Toast 组件 ──

function ToastContainer({ toasts }: { toasts: Toast[] }) {
  if (toasts.length === 0) return null;
  return (
    <div className="toast-container">
      {toasts.map((t) => (
        <div key={t.id} className={`toast toast--${t.level}`}>
          <span className={`toast-icon toast-icon--${t.level}`}>
            {t.level === 'error' ? '✕' : t.level === 'warning' ? '⚠' : 'ℹ'}
          </span>
          <span className="toast-message">{t.message}</span>
        </div>
      ))}
    </div>
  );
}
