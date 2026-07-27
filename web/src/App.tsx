/**
 * fishtty PWA — App 根组件。
 *
 * 包含：设备列表页 → 终端会话页，
 * Session 选项卡切换、断线重连遮罩、错误 Toast。
 * 集成移动端键盘适配与本地回显优化。
 */

import { useState, useRef, useCallback, useEffect } from 'react';
import type { TunnelMessage } from '@/gen/fishtty/v1/tunnel_pb';
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
    localStorage.getItem(LS_DEVICE_ID_KEY) || ''
  );
  const [wsState, setWsState] = useState<WsState>('WS_DISCONNECTED');
  const [errors, setErrors] = useState<string[]>([]);
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
        },
        onError(err: Error) {
          addError(err.message);
        },
      });

      clientRef.current = client;
      client.connect();
      setDeviceId(targetDeviceId);
    },
    [serverUrl]
  );

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
            // 通过本地回显缓冲区过滤服务端回显重叠
            const writeData = handle.drainEcho(chunk.data);
            if (writeData.length > 0) {
              handle.term.write(writeData);
            }
          }
          break;
        }

        case 'sessionCreated': {
          const created = msg.payload.value;
          if (created.status !== 1) {
            addError(`会话创建失败: ${created.message || '未知错误'}`);
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
          addError(`[${err.code}] ${err.message}`);
          break;
        }

        default:
          break;
      }
    },
    []
  );

  // ── WS 连上时自动切到终端页 ──
  useEffect(() => {
    if (wsState === 'WS_ACTIVE' && view === 'devices') {
      setView('terminal');
    }
  }, [wsState, view]);

  // ── 错误提示 ──
  const addError = useCallback((msg: string) => {
    setErrors((prev) => [...prev.slice(-4), msg]);
    setTimeout(() => {
      setErrors((prev) => prev.filter((e) => e !== msg));
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
      addError('发送 SessionInit 失败，请刷新页面重试');
    }
  }, [deviceId, createSession, addError]);

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

        <ErrorToast errors={errors} />
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

      {/* 错误提示 */}
      <ErrorToast errors={errors} />
    </div>
  );
}

// ── 错误 Toast 组件 ──

function ErrorToast({ errors }: { errors: string[] }) {
  if (errors.length === 0) return null;
  return (
    <div className="error-toast-container">
      {errors.map((msg, i) => (
        <div key={i} className="error-toast">
          {msg}
        </div>
      ))}
    </div>
  );
}
