/**
 * fishtty WebSocket 客户端
 *
 * 管理到 fishtty-server 的 WebSocket 连接。
 * 使用 Protobuf 二进制帧收发 TunnelMessage，
 * 实现断线自动重连、应用层心跳、连接超时检测和 Reattach 会话恢复。
 */

import { create, fromBinary, toBinary } from '@bufbuild/protobuf';
import {
  TunnelMessageSchema,
  DataChunkSchema,
  ReattachSchema,
  SessionInitSchema,
  ResizeSchema,
  SessionDestroySchema,
  type TunnelMessage,
} from '@/gen/fishtty/v1/tunnel_pb';

// ── 连接状态 ──

export type WsState =
  | 'WS_DISCONNECTED'
  | 'WS_CONNECTING'
  | 'WS_ACTIVE'
  | 'WS_RECONNECTING';

// ── 回调类型 ──

export interface WsCallbacks {
  onMessage: (msg: TunnelMessage) => void;
  onStateChange: (state: WsState) => void;
  onError: (error: Error) => void;
}

// ── 常量 ──

/** 重连最小延迟 (ms) */
const RECONNECT_MIN_DELAY = 1000;
/** 重连默认最大延迟 (ms) */
const RECONNECT_MAX_DELAY = 10000;
/** 重连风暴时最大延迟 (ms) */
const RECONNECT_STORM_MAX_DELAY = 30000;
/** 连接建立超时 (ms) */
const CONNECT_TIMEOUT = 10000;
/** 应用层心跳间隔 (ms) */
const PING_INTERVAL = 30000;
/** Pong 超时 (ms) */
const PONG_TIMEOUT = 10000;
/** 重连风暴阈值：60s 内断连次数 */
const STORM_WINDOW_MS = 60000;
/** 重连风暴阈值：60s 内超过此次数触发保护 */
const STORM_THRESHOLD = 5;

// ── localStorage 键 ──

const LS_DEVICE_ID_KEY = 'fishtty_device_id';
const LS_LAST_ACTIVE_KEY = 'fishtty_last_active';

// ── WebSocket 管理器 ──

export class FishTTYClient {
  private ws: WebSocket | null = null;
  private state: WsState = 'WS_DISCONNECTED';
  private serverUrl: string;
  private deviceId: string;
  private callbacks: WsCallbacks;

  /** 每个活跃 session 最后确认的 seq（用于 Reattach） */
  private lastAckSeq: Map<string, number> = new Map();

  /** 重连退避计数器 */
  private backoffDelay = RECONNECT_MIN_DELAY;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;

  /** 连接超时定时器 */
  private connectTimeoutTimer: ReturnType<typeof setTimeout> | null = null;

  /** 应用层心跳定时器 */
  private pingTimer: ReturnType<typeof setInterval> | null = null;
  private pongTimeoutTimer: ReturnType<typeof setTimeout> | null = null;

  /** 重连风暴保护：记录最近断连时间戳 */
  private disconnectTimestamps: number[] = [];
  private stormProtection = false;

  /** 活跃 session IDs */
  private activeSessions: Set<string> = new Set();

  constructor(serverUrl: string, deviceId: string, callbacks: WsCallbacks) {
    this.serverUrl = serverUrl;
    this.deviceId = deviceId;
    this.callbacks = callbacks;
  }

  // ── 公开 API ──

  /** 建立 WebSocket 连接 */
  connect(): void {
    if (this.state === 'WS_CONNECTING' || this.state === 'WS_ACTIVE') {
      return;
    }
    this.setState('WS_CONNECTING');
    this.doConnect();
  }

  /** 断开连接 */
  disconnect(): void {
    this.clearReconnectTimer();
    this.clearConnectTimeout();
    this.clearPingTimers();
    if (this.ws) {
      this.ws.close(1000, '客户端主动断开');
      this.ws = null;
    }
    this.setState('WS_DISCONNECTED');
  }

  /** 发送 TunnelMessage（二进制帧） */
  send(msg: TunnelMessage): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return false;
    }
    try {
      const data = toBinary(TunnelMessageSchema, msg);
      this.ws.send(data);
      return true;
    } catch (err) {
      console.error('[fishtty] 发送消息失败:', err);
      return false;
    }
  }

  /** 创建新 PTY 会话 */
  createSession(sessionId: string, cols: number, rows: number): boolean {
    this.activeSessions.add(sessionId);
    this.lastAckSeq.set(sessionId, 0);

    const msg = create(TunnelMessageSchema, {
      sessionId,
      payload: {
        case: 'sessionInit',
        value: create(SessionInitSchema, {
          sessionId,
          cols,
          rows,
          command: '',
        }),
      },
    });
    this.persistActiveState();
    return this.send(msg);
  }

  /** 发送 Resize 消息 */
  sendResize(sessionId: string, cols: number, rows: number): boolean {
    const msg = create(TunnelMessageSchema, {
      sessionId,
      payload: {
        case: 'resize',
        value: create(ResizeSchema, { sessionId, cols, rows }),
      },
    });
    return this.send(msg);
  }

  /** 发送数据到 PTY（stdin） */
  sendData(sessionId: string, data: Uint8Array): boolean {
    const msg = create(TunnelMessageSchema, {
      sessionId,
      payload: {
        case: 'dataChunk',
        value: create(DataChunkSchema, {
          sessionId,
          seq: 0n, // Mobile→Server 方向 seq=0
          data,
        }),
      },
    });
    return this.send(msg);
  }

  /** 销毁会话 */
  destroySession(sessionId: string): boolean {
    this.activeSessions.delete(sessionId);
    this.lastAckSeq.delete(sessionId);
    this.persistActiveState();

    const msg = create(TunnelMessageSchema, {
      sessionId,
      payload: {
        case: 'sessionDestroy',
        value: create(SessionDestroySchema, { sessionId }),
      },
    });
    return this.send(msg);
  }

  /** 获取连接状态 */
  getState(): WsState {
    return this.state;
  }

  /** 是否有活跃 session */
  hasActiveSessions(): boolean {
    return this.activeSessions.size > 0;
  }

  /** 获取 deviceId */
  getDeviceId(): string {
    return this.deviceId;
  }

  /** 更新 session 的 last_ack_seq */
  updateLastAckSeq(sessionId: string, seq: number): void {
    const current = this.lastAckSeq.get(sessionId) ?? 0;
    if (seq > current) {
      this.lastAckSeq.set(sessionId, seq);
    }
  }

  // ── 内部实现 ──

  private doConnect(): void {
    // 构建 WebSocket URL：wss://host/ws?device_id=xxx
    const wsUrl = this.serverUrl
      .replace(/^http/, 'ws')
      .replace(/\/$/, '') + `/ws?device_id=${encodeURIComponent(this.deviceId)}`;

    // 关闭旧连接（避免重连时的连接泄漏）
    if (this.ws) {
      this.ws.onclose = null; // 阻止旧连接的 onclose 触发重连
      this.ws.onerror = null;
      this.ws.close(1000, '重连替换');
      this.ws = null;
    }

    this.ws = new WebSocket(wsUrl, ['fish-tty-v1']);
    this.ws.binaryType = 'arraybuffer';

    // ── 连接超时检测 ──
    this.clearConnectTimeout();
    this.connectTimeoutTimer = setTimeout(() => {
      if (this.ws && this.ws.readyState !== WebSocket.OPEN) {
        console.warn('[fishtty] WebSocket 连接超时 (10s)');
        this.callbacks.onError(new Error('[连接错误] 连接超时，请检查网络和服务器地址'));
        this.ws.close(4000, '连接超时');
        this.ws = null;
      }
    }, CONNECT_TIMEOUT);

    // ── 连接打开 ──
    this.ws.onopen = () => {
      this.clearConnectTimeout();
      this.setState('WS_ACTIVE');
      this.backoffDelay = RECONNECT_MIN_DELAY;
      this.stormProtection = false;

      // 持久化连接信息
      this.persistConnectionInfo();

      // 启动应用层心跳
      this.startPingPong();

      // 重连后对所有活跃 session 发送 Reattach
      this.activeSessions.forEach((sid) => {
        const lastSeq = this.lastAckSeq.get(sid) ?? 0;
        const msg = create(TunnelMessageSchema, {
          sessionId: sid,
          payload: {
            case: 'reattach',
            value: create(ReattachSchema, {
              sessionId: sid,
              lastAckSeq: BigInt(lastSeq),
            }),
          },
        });
        this.send(msg);
      });
    };

    // ── 消息接收 ──
    this.ws.onmessage = (event: MessageEvent) => {
      // 处理应用层 pong 文本帧
      if (typeof event.data === 'string') {
        if (event.data === 'pong') {
          this.clearPongTimeout();
          return;
        }
        return;
      }

      if (!(event.data instanceof ArrayBuffer)) {
        console.warn('[fishtty] 收到非二进制帧，已忽略');
        return;
      }

      try {
        const msg = fromBinary(TunnelMessageSchema, new Uint8Array(event.data));
        // 跟踪 DataChunk 的 seq
        if (msg.payload.case === 'dataChunk') {
          const chunk = msg.payload.value;
          if (chunk.seq > 0n) {
            this.updateLastAckSeq(msg.sessionId, Number(chunk.seq));
          }
        }
        // 跟踪会话销毁
        if (msg.payload.case === 'sessionDestroyed') {
          this.activeSessions.delete(msg.sessionId);
          this.lastAckSeq.delete(msg.sessionId);
          this.persistActiveState();
        }
        this.callbacks.onMessage(msg);
      } catch (err) {
        console.error('[fishtty] Protobuf 反序列化失败:', err);
      }
    };

    // ── 连接关闭 ──
    this.ws.onclose = (_event: CloseEvent) => {
      this.clearPingTimers();
      this.clearConnectTimeout();
      this.ws = null;

      // 记录断连时间戳（用于风暴检测）
      const now = Date.now();
      this.disconnectTimestamps.push(now);
      // 只保留最近 60s 的记录
      this.disconnectTimestamps = this.disconnectTimestamps.filter(
        (t) => now - t < STORM_WINDOW_MS,
      );

      if (this.state === 'WS_ACTIVE' || this.state === 'WS_CONNECTING') {
        this.setState('WS_RECONNECTING');
        this.scheduleReconnect();
      }
    };

    // ── 连接错误 ──
    this.ws.onerror = () => {
      // 尝试从 WebSocket 状态提取错误信息
      const readyState = this.ws?.readyState;
      let errorMsg = '[连接错误] WebSocket 连接失败';

      if (readyState === WebSocket.CLOSED || readyState === WebSocket.CLOSING) {
        errorMsg = '[连接错误] 服务器拒绝连接，请检查 device_id 是否已注册';
      }

      // onclose 会在 onerror 后触发，但这里先通知用户
      this.callbacks.onError(new Error(errorMsg));
    };
  }

  // ── 重连逻辑 ──

  private scheduleReconnect(): void {
    this.clearReconnectTimer();

    // 重连风暴检测
    if (!this.stormProtection && this.disconnectTimestamps.length >= STORM_THRESHOLD) {
      this.stormProtection = true;
      this.callbacks.onError(
        new Error('[连接不稳定] 频繁断连，已降低重连频率，请检查网络状况'),
      );
      console.warn('[fishtty] 检测到重连风暴，退避上限提升至 30s');
    }

    const maxDelay = this.stormProtection ? RECONNECT_STORM_MAX_DELAY : RECONNECT_MAX_DELAY;
    const delay = Math.min(this.backoffDelay, maxDelay);

    console.log(`[fishtty] ${delay}ms 后尝试重连...`);
    this.reconnectTimer = setTimeout(() => {
      this.setState('WS_CONNECTING');
      this.doConnect();
    }, delay);
    this.backoffDelay = Math.min(this.backoffDelay * 2, maxDelay);
  }

  // ── 应用层 Ping/Pong ──

  private startPingPong(): void {
    this.clearPingTimers();

    // 每 30s 发送 ping
    this.pingTimer = setInterval(() => {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
      try {
        this.ws.send('ping');
        // 启动 pong 超时检测
        this.pongTimeoutTimer = setTimeout(() => {
          console.warn('[fishtty] Pong 超时 (10s)，视连接为断开');
          this.callbacks.onError(new Error('[连接中断] 网络心跳超时，正在重连...'));
          if (this.ws) {
            this.ws.close(4001, 'Pong 超时');
            this.ws = null;
          }
        }, PONG_TIMEOUT);
      } catch {
        // send 失败，ws 可能已关闭
      }
    }, PING_INTERVAL);
  }

  private clearPingTimers(): void {
    if (this.pingTimer) {
      clearInterval(this.pingTimer);
      this.pingTimer = null;
    }
    this.clearPongTimeout();
  }

  private clearPongTimeout(): void {
    if (this.pongTimeoutTimer) {
      clearTimeout(this.pongTimeoutTimer);
      this.pongTimeoutTimer = null;
    }
  }

  // ── localStorage 持久化 ──

  /** 持久化连接信息 */
  private persistConnectionInfo(): void {
    try {
      localStorage.setItem(LS_DEVICE_ID_KEY, this.deviceId);
      localStorage.setItem(LS_LAST_ACTIVE_KEY, Date.now().toString());
    } catch {
      // localStorage 不可用（无痕模式等），静默忽略
    }
  }

  /** 持久化活跃 session 信息 */
  private persistActiveState(): void {
    try {
      if (this.activeSessions.size > 0) {
        localStorage.setItem(LS_LAST_ACTIVE_KEY, Date.now().toString());
      }
    } catch {
      // localStorage 不可用，静默忽略
    }
  }

  /** 清除持久化状态 */
  static clearPersistedState(): void {
    try {
      localStorage.removeItem(LS_DEVICE_ID_KEY);
      localStorage.removeItem(LS_LAST_ACTIVE_KEY);
    } catch {
      // 静默忽略
    }
  }

  /** 检查是否有持久化的历史连接记录 */
  static hasPersistedConnection(): boolean {
    try {
      return !!localStorage.getItem(LS_LAST_ACTIVE_KEY);
    } catch {
      return false;
    }
  }

  /** 获取持久化的 deviceId */
  static getPersistedDeviceId(): string {
    try {
      return localStorage.getItem(LS_DEVICE_ID_KEY) || '';
    } catch {
      return '';
    }
  }

  // ── 辅助方法 ──

  private clearReconnectTimer(): void {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private clearConnectTimeout(): void {
    if (this.connectTimeoutTimer) {
      clearTimeout(this.connectTimeoutTimer);
      this.connectTimeoutTimer = null;
    }
  }

  private setState(newState: WsState): void {
    if (this.state !== newState) {
      this.state = newState;
      this.callbacks.onStateChange(newState);
    }
  }
}
