/**
 * VirtualKeyboard — 移动端虚拟按键工具条。
 *
 * 提供终端常用按键（Esc、Tab、Ctrl+C、方向键）和粘贴按钮。
 * 方向键支持长按连续触发，粘贴按钮自动包裹括号粘贴转义序列。
 */

import { useState, useRef, useCallback, useEffect } from 'react';
import {
  VIRTUAL_KEYBOARD_KEYS,
  wrapBracketedPaste,
  type KeyMapping,
} from './keymap';
import type { FishTTYClient } from '@/ws/client';

// ── 常量 ──

/** 长按初始延迟（ms） */
const LONG_PRESS_DELAY = 500;
/** 长按重复率（Hz） */
const LONG_PRESS_RATE = 10;
/** 重复间隔（ms） */
const REPEAT_INTERVAL = 1000 / LONG_PRESS_RATE;

// ── Props ──

interface VirtualKeyboardProps {
  /** 当前活跃 session ID */
  sessionId: string;
  /** WebSocket 客户端 */
  client: FishTTYClient;
}

// ── 组件 ──

export default function VirtualKeyboard({ sessionId, client }: VirtualKeyboardProps) {
  return (
    <div className="virtual-keyboard">
      {VIRTUAL_KEYBOARD_KEYS.map((key, i) => (
        <KeyButton
          key={`${key.label}-${i}`}
          mapping={key}
          sessionId={sessionId}
          client={client}
        />
      ))}
    </div>
  );
}

// ── 单个按键 ──

function KeyButton({
  mapping,
  sessionId,
  client,
}: {
  mapping: KeyMapping;
  sessionId: string;
  client: FishTTYClient;
}) {
  const [pressing, setPressing] = useState(false);
  const longPressTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const repeatTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const isLongPressRef = useRef(false);

  // 清理定时器
  const clearTimers = useCallback(() => {
    if (longPressTimerRef.current) {
      clearTimeout(longPressTimerRef.current);
      longPressTimerRef.current = null;
    }
    if (repeatTimerRef.current) {
      clearInterval(repeatTimerRef.current);
      repeatTimerRef.current = null;
    }
  }, []);

  useEffect(() => {
    return clearTimers;
  }, [clearTimers]);

  // 发送按键
  const sendKey = useCallback(() => {
    // 特殊：粘贴按钮
    if (mapping.key === '__paste__') {
      handlePaste(sessionId, client);
      return;
    }
    client.sendData(sessionId, mapping.bytes);
  }, [mapping, sessionId, client]);

  // 触摸/鼠标按下
  const handleDown = useCallback(() => {
    setPressing(true);
    isLongPressRef.current = false;

    // 立即发送一次
    sendKey();

    // 方向键（key 为 ArrowUp/Down/Left/Right）启动长按
    if (
      mapping.key &&
      ['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight'].includes(mapping.key)
    ) {
      longPressTimerRef.current = setTimeout(() => {
        isLongPressRef.current = true;
        // 启动连续重复
        repeatTimerRef.current = setInterval(() => {
          sendKey();
        }, REPEAT_INTERVAL);
      }, LONG_PRESS_DELAY);
    }
  }, [mapping, sendKey]);

  // 触摸/鼠标松开
  const handleUp = useCallback(() => {
    setPressing(false);
    clearTimers();
  }, [clearTimers]);

  return (
    <button
      className={`vk-btn ${pressing ? 'vk-btn--active' : ''} ${mapping.key === '__paste__' ? 'vk-btn--paste' : ''}`}
      onPointerDown={handleDown}
      onPointerUp={handleUp}
      onPointerLeave={handleUp}
      onPointerCancel={handleUp}
      aria-label={mapping.label}
      title={mapping.label}
    >
      {mapping.label}
    </button>
  );
}

// ── 粘贴处理 ──

async function handlePaste(sessionId: string, client: FishTTYClient): Promise<void> {
  try {
    const text = await navigator.clipboard.readText();
    if (!text) return;

    // 括号粘贴模式：通知终端这是一次粘贴操作
    const wrapped = wrapBracketedPaste(text);
    const encoder = new TextEncoder();
    client.sendData(sessionId, encoder.encode(wrapped));
  } catch (err) {
    console.warn('[fishtty] 读取剪贴板失败:', err);
    // 降级：尝试使用旧 API
    try {
      // 某些环境下 clipboard API 不可用
      const text = prompt('请粘贴文本:');
      if (text) {
        const wrapped = wrapBracketedPaste(text);
        const encoder = new TextEncoder();
        client.sendData(sessionId, encoder.encode(wrapped));
      }
    } catch {
      // 忽略
    }
  }
}
