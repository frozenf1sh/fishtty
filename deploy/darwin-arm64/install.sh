#!/bin/bash
# fishtty-agent macOS 一键安装脚本
# 用法: chmod +x install.sh && ./install.sh
set -euo pipefail

echo "=== fishtty-agent 安装 (macOS) ==="

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# ── 安装二进制 ──
echo "→ 安装二进制..."
sudo cp "$SCRIPT_DIR/fishtty-agent" /usr/local/bin/fishtty-agent
sudo chmod +x /usr/local/bin/fishtty-agent
echo "  ✓ /usr/local/bin/fishtty-agent"

# ── 安装配置 ──
echo "→ 安装配置文件..."
sudo mkdir -p /usr/local/etc/fishtty
if [ ! -f /usr/local/etc/fishtty/fishtty-agent.yaml ]; then
    sudo cp "$SCRIPT_DIR/fishtty-agent.yaml" /usr/local/etc/fishtty/fishtty-agent.yaml
    echo "  ✓ /usr/local/etc/fishtty/fishtty-agent.yaml (新文件，请编辑 server 和 token)"
else
    echo "  ⚠ /usr/local/etc/fishtty/fishtty-agent.yaml 已存在，跳过覆盖"
fi

# ── 日志目录 ──
sudo mkdir -p /usr/local/var/log

# ── 安装 launchd 服务 ──
echo "→ 安装 launchd 服务..."
cp "$SCRIPT_DIR/com.fishtty.agent.plist" "$HOME/Library/LaunchAgents/com.fishtty.agent.plist"
echo "  ✓ ~/Library/LaunchAgents/com.fishtty.agent.plist"

# ── 完成 ──
echo ""
echo "=== 安装完成 ==="
echo ""
echo "下一步："
echo "  1. 编辑配置: vim /usr/local/etc/fishtty/fishtty-agent.yaml"
echo "     - 修改 server 为你的 fishtty-server 地址"
echo "     - 修改 token 为你的认证令牌"
echo ""
echo "  2. 启动服务: launchctl load ~/Library/LaunchAgents/com.fishtty.agent.plist"
echo "  3. 查看状态: launchctl list | grep fishtty"
echo "  4. 查看日志: tail -f /usr/local/var/log/fishtty-agent.log"
echo ""
echo "管理命令："
echo "  launchctl unload ~/Library/LaunchAgents/com.fishtty.agent.plist  # 停止"
echo "  launchctl load ~/Library/LaunchAgents/com.fishtty.agent.plist    # 启动"
echo ""
echo "💡 开机自启：launchd 的 RunAtLoad 已启用，重启后自动运行。"
