/**
 * fishtty WebSocket 客户端
 *
 * 管理到 fishtty-server 的 WebSocket 连接。
 * 使用 Protobuf 二进制帧收发 TunnelMessage，
 * 实现断线自动重连和 Reattach 会话恢复。
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

/** 重连最小延迟 */
const RECONNECT_MIN_DELAY = 1000;
/** 重连最大延迟 */
const RECONNECT_MAX_DELAY = 10000;

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
      this.ws.close(1000, '重连替换');
      this.ws = null;
    }

    this.ws = new WebSocket(wsUrl, ['fish-tty-v1']);
    this.ws.binaryType = 'arraybuffer';

    this.ws.onopen = () => {
      this.setState('WS_ACTIVE');
      this.backoffDelay = RECONNECT_MIN_DELAY;

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

    this.ws.onmessage = (event: MessageEvent) => {
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
        }
        this.callbacks.onMessage(msg);
      } catch (err) {
        console.error('[fishtty] Protobuf 反序列化失败:', err);
      }
    };

    this.ws.onclose = (_event) => {
      this.ws = null;
      if (this.state === 'WS_ACTIVE' || this.state === 'WS_CONNECTING') {
        this.setState('WS_RECONNECTING');
        this.scheduleReconnect();
      }
    };

    this.ws.onerror = () => {
      // onclose 会在 onerror 后触发
    };
  }

  private scheduleReconnect(): void {
    this.clearReconnectTimer();
    console.log(`[fishtty] ${this.backoffDelay}ms 后尝试重连...`);
    this.reconnectTimer = setTimeout(() => {
      this.doConnect();
    }, this.backoffDelay);
    this.backoffDelay = Math.min(this.backoffDelay * 2, RECONNECT_MAX_DELAY);
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private setState(newState: WsState): void {
    if (this.state !== newState) {
      this.state = newState;
      this.callbacks.onStateChange(newState);
    }
  }
}
