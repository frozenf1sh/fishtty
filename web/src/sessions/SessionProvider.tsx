/**
 * SessionProvider — React Context 管理活跃的 PTY 会话。
 *
 * 维护每个 device 下的 session 列表、当前活跃 session、
 * 以及每个 session 的 last_ack_seq（用于 Reattach）。
 */

import {
  createContext,
  useContext,
  useState,
  useCallback,
  type ReactNode,
} from 'react';

// ── 类型 ──

export interface SessionInfo {
  sessionId: string;
  deviceId: string;
  createdAt: number;
}

export interface SessionContextType {
  /** 当前设备的所有活跃 session */
  sessions: SessionInfo[];
  /** 当前选中的 session ID */
  activeSessionId: string | null;
  /** 创建新 session */
  createSession: (deviceId: string) => string;
  /** 切换到指定 session */
  switchSession: (sessionId: string) => void;
  /** 移除 session */
  removeSession: (sessionId: string) => void;
  /** 当前连接的设备 ID */
  connectedDeviceId: string | null;
  /** 连接到设备 */
  connectToDevice: (deviceId: string) => void;
  /** 断开设备 */
  disconnectDevice: () => void;
}

const SessionContext = createContext<SessionContextType | null>(null);

// ── ID 生成 ──

let sessionCounter = 0;
function generateSessionId(): string {
  sessionCounter++;
  return `session-${Date.now()}-${sessionCounter}`;
}

// ── Provider ──

export function SessionProvider({ children }: { children: ReactNode }) {
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [connectedDeviceId, setConnectedDeviceId] = useState<string | null>(null);

  const createSession = useCallback((deviceId: string): string => {
    const sid = generateSessionId();
    const info: SessionInfo = {
      sessionId: sid,
      deviceId,
      createdAt: Date.now(),
    };
    setSessions((prev) => [...prev, info]);
    setActiveSessionId(sid);
    return sid;
  }, []);

  const switchSession = useCallback((sessionId: string) => {
    setActiveSessionId(sessionId);
  }, []);

  const removeSession = useCallback((sessionId: string) => {
    setSessions((prev) => {
      const filtered = prev.filter((s) => s.sessionId !== sessionId);
      if (sessionId === activeSessionId && filtered.length > 0) {
        setActiveSessionId(filtered[filtered.length - 1].sessionId);
      } else if (filtered.length === 0) {
        setActiveSessionId(null);
      }
      return filtered;
    });
  }, [activeSessionId]);

  const connectToDevice = useCallback((deviceId: string) => {
    setConnectedDeviceId(deviceId);
    setSessions([]);
    setActiveSessionId(null);
  }, []);

  const disconnectDevice = useCallback(() => {
    setConnectedDeviceId(null);
    setSessions([]);
    setActiveSessionId(null);
  }, []);

  return (
    <SessionContext.Provider
      value={{
        sessions,
        activeSessionId,
        createSession,
        switchSession,
        removeSession,
        connectedDeviceId,
        connectToDevice,
        disconnectDevice,
      }}
    >
      {children}
    </SessionContext.Provider>
  );
}

// ── Hook ──

export function useSession(): SessionContextType {
  const ctx = useContext(SessionContext);
  if (!ctx) {
    throw new Error('useSession 必须在 SessionProvider 内部使用');
  }
  return ctx;
}
