/**
 * fishtty 按键映射表
 *
 * 定义物理/虚拟按键到 ANSI/ASCII 字节序列的映射。
 * 所有特殊键通过向 PTY master fd 写入对应字节序列来注入，
 * 利用终端行规程（line discipline）处理信号转换（如 \x03 → SIGINT）。
 */

/** 按键映射条目 */
export interface KeyMapping {
  /** 显示给用户的标签 */
  label: string;
  /** 发送到 PTY 的字节序列 */
  bytes: Uint8Array;
  /** 可选：键盘事件的 key 值（用于物理键盘匹配） */
  key?: string;
  /** 可选：是否需要 Ctrl 修饰键 */
  ctrlKey?: boolean;
  /** 可选：是否需要 Alt 修饰键 */
  altKey?: boolean;
}

/** 控制字符常量 */
const CTRL = {
  /** Ctrl+C → SIGINT */
  C: 0x03,
  /** Ctrl+D → EOF */
  D: 0x04,
  /** Ctrl+Z → SIGTSTP（挂起） */
  Z: 0x1a,
  /** Ctrl+\ → SIGQUIT */
  BACKSLASH: 0x1c,
  /** Ctrl+U → 删到行首 */
  U: 0x15,
  /** Ctrl+W → 删前一个词 */
  W: 0x17,
  /** Ctrl+L → 清屏 */
  L: 0x0c,
} as const;

/** 辅助函数：从字符串创建 Uint8Array */
function bytes(s: string): Uint8Array {
  return new TextEncoder().encode(s);
}

// =================================================================
// 虚拟键盘栏按钮定义
// =================================================================

/** 虚拟键盘栏显示的按钮列表（按顺序渲染） */
export const VIRTUAL_KEYBOARD_KEYS: KeyMapping[] = [
  { label: 'Esc', bytes: new Uint8Array([0x1b]), key: 'Escape' },
  { label: 'Tab', bytes: new Uint8Array([0x09]), key: 'Tab' },
  { label: 'Ctrl+C', bytes: new Uint8Array([CTRL.C]), key: 'c', ctrlKey: true },
  { label: '▲', bytes: bytes('\x1b[A'), key: 'ArrowUp' },
  { label: '▼', bytes: bytes('\x1b[B'), key: 'ArrowDown' },
  { label: '◀', bytes: bytes('\x1b[D'), key: 'ArrowLeft' },
  { label: '▶', bytes: bytes('\x1b[C'), key: 'ArrowRight' },
  { label: '📋', bytes: new Uint8Array(), key: '__paste__' },
];

// =================================================================
// 完整按键映射表（物理键盘 + 额外虚拟键）
// =================================================================

export const KEY_MAP: Record<string, KeyMapping> = {
  // ── 控制字符 ──
  'ctrl+c': { label: 'Ctrl+C', bytes: new Uint8Array([CTRL.C]), key: 'c', ctrlKey: true },
  'ctrl+d': { label: 'Ctrl+D', bytes: new Uint8Array([CTRL.D]), key: 'd', ctrlKey: true },
  'ctrl+z': { label: 'Ctrl+Z', bytes: new Uint8Array([CTRL.Z]), key: 'z', ctrlKey: true },
  'ctrl+backslash': { label: 'Ctrl+\\', bytes: new Uint8Array([CTRL.BACKSLASH]), key: '\\', ctrlKey: true },
  'ctrl+u': { label: 'Ctrl+U', bytes: new Uint8Array([CTRL.U]), key: 'u', ctrlKey: true },
  'ctrl+w': { label: 'Ctrl+W', bytes: new Uint8Array([CTRL.W]), key: 'w', ctrlKey: true },
  'ctrl+l': { label: 'Ctrl+L', bytes: new Uint8Array([CTRL.L]), key: 'l', ctrlKey: true },

  // ── 编辑键 ──
  'escape': { label: 'Esc', bytes: new Uint8Array([0x1b]), key: 'Escape' },
  'tab': { label: 'Tab', bytes: new Uint8Array([0x09]), key: 'Tab' },
  'enter': { label: 'Enter', bytes: new Uint8Array([0x0d]), key: 'Enter' },
  'backspace': { label: 'BS', bytes: new Uint8Array([0x7f]), key: 'Backspace' },

  // ── 方向键 ──
  'arrow_up': { label: '▲', bytes: bytes('\x1b[A'), key: 'ArrowUp' },
  'arrow_down': { label: '▼', bytes: bytes('\x1b[B'), key: 'ArrowDown' },
  'arrow_right': { label: '▶', bytes: bytes('\x1b[C'), key: 'ArrowRight' },
  'arrow_left': { label: '◀', bytes: bytes('\x1b[D'), key: 'ArrowLeft' },

  // ── 导航键 ──
  'home': { label: 'Home', bytes: bytes('\x1b[H'), key: 'Home' },
  'end': { label: 'End', bytes: bytes('\x1b[F'), key: 'End' },
  'page_up': { label: 'PgUp', bytes: bytes('\x1b[5~'), key: 'PageUp' },
  'page_down': { label: 'PgDn', bytes: bytes('\x1b[6~'), key: 'PageDown' },
  'delete': { label: 'Del', bytes: bytes('\x1b[3~'), key: 'Delete' },
  'insert': { label: 'Ins', bytes: bytes('\x1b[2~'), key: 'Insert' },

  // ── 功能键 ──
  'f1': { label: 'F1', bytes: bytes('\x1bOP'), key: 'F1' },
  'f2': { label: 'F2', bytes: bytes('\x1bOQ'), key: 'F2' },
  'f3': { label: 'F3', bytes: bytes('\x1bOR'), key: 'F3' },
  'f4': { label: 'F4', bytes: bytes('\x1bOS'), key: 'F4' },
  'f5': { label: 'F5', bytes: bytes('\x1b[15~'), key: 'F5' },
  'f6': { label: 'F6', bytes: bytes('\x1b[17~'), key: 'F6' },
  'f7': { label: 'F7', bytes: bytes('\x1b[18~'), key: 'F7' },
  'f8': { label: 'F8', bytes: bytes('\x1b[19~'), key: 'F8' },
  'f9': { label: 'F9', bytes: bytes('\x1b[20~'), key: 'F9' },
  'f10': { label: 'F10', bytes: bytes('\x1b[21~'), key: 'F10' },
  'f11': { label: 'F11', bytes: bytes('\x1b[23~'), key: 'F11' },
  'f12': { label: 'F12', bytes: bytes('\x1b[24~'), key: 'F12' },
};

// =================================================================
// 粘贴相关常量
// =================================================================

/** 括号粘贴模式起始序列（通知终端应用：以下是粘贴内容） */
export const BRACKETED_PASTE_START = '\x1b[200~';

/** 括号粘贴模式结束序列 */
export const BRACKETED_PASTE_END = '\x1b[201~';

/**
 * 包装粘贴内容，添加括号粘贴模式标记。
 * 支持括号粘贴的应用（bash、vim、zsh 等）会将
 * 标记之间的内容视为字面粘贴，不做特殊字符解释。
 */
export function wrapBracketedPaste(text: string): string {
  return BRACKETED_PASTE_START + text + BRACKETED_PASTE_END;
}

// =================================================================
// 键盘事件匹配
// =================================================================

/**
 * 根据浏览器 KeyboardEvent 查找匹配的按键映射。
 * 返回匹配的 KeyMapping，或 null（让浏览器/xterm.js 自行处理）。
 */
export function matchKeyEvent(e: KeyboardEvent): KeyMapping | null {
  // 忽略纯修饰键（仅 Ctrl/Alt/Shift/Meta 按下）
  if (['Control', 'Alt', 'Shift', 'Meta'].includes(e.key)) {
    return null;
  }

  // 构建查找键（如 "ctrl+c"、"arrow_up"）
  const parts: string[] = [];
  if (e.ctrlKey) parts.push('ctrl');
  if (e.altKey) parts.push('alt');
  if (e.metaKey) parts.push('meta');

  // 对于字母键，使用小写形式
  const keyName = e.key.length === 1 ? e.key.toLowerCase() : e.key.toLowerCase().replace(/\s/g, '_');
  parts.push(keyName);
  const lookupKey = parts.join('_');

  // 先精确匹配带修饰键的组合
  const exact = KEY_MAP[lookupKey];
  if (exact) return exact;

  // 再匹配不带修饰键的版本
  const plain = KEY_MAP[keyName];
  if (plain && !e.ctrlKey && !e.altKey && !e.metaKey) return plain;

  return null;
}
