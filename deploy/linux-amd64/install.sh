#!/bin/bash
# fishtty-agent Linux 一键安装脚本
# 用法: chmod +x install.sh && sudo ./install.sh
set -euo pipefail

echo "=== fishtty-agent 安装 (Linux) ==="

# ── 检查 ──
if [ "$(id -u)" -ne 0 ]; then
    echo "❌ 请用 sudo 运行此脚本"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# ── 安装二进制 ──
echo "→ 安装二进制..."
cp "$SCRIPT_DIR/fishtty-agent" /usr/local/bin/fishtty-agent
chmod +x /usr/local/bin/fishtty-agent
echo "  ✓ /usr/local/bin/fishtty-agent"

# ── 安装配置 ──
echo "→ 安装配置文件..."
mkdir -p /etc/fishtty
if [ ! -f /etc/fishtty/fishtty-agent.yaml ]; then
    cp "$SCRIPT_DIR/fishtty-agent.yaml" /etc/fishtty/fishtty-agent.yaml
    echo "  ✓ /etc/fishtty/fishtty-agent.yaml (新文件，请编辑 server 和 token)"
else
    echo "  ⚠ /etc/fishtty/fishtty-agent.yaml 已存在，跳过覆盖"
fi

# ── 安装 systemd 服务 ──
echo "→ 安装 systemd 服务..."
cp "$SCRIPT_DIR/fishtty-agent.service" /etc/systemd/system/fishtty-agent.service
systemctl daemon-reload
echo "  ✓ /etc/systemd/system/fishtty-agent.service"

# ── 完成 ──
echo ""
echo "=== 安装完成 ==="
echo ""
echo "下一步："
echo "  1. 编辑配置: sudo vim /etc/fishtty/fishtty-agent.yaml"
echo "     - 修改 server 为你的 fishtty-server 地址"
echo "     - 修改 token 为你的认证令牌"
echo ""
echo "  2. 启动服务: sudo systemctl enable --now fishtty-agent"
echo "  3. 查看状态: sudo systemctl status fishtty-agent"
echo "  4. 查看日志: sudo journalctl -u fishtty-agent -f"
echo ""
echo "管理命令："
echo "  sudo systemctl restart fishtty-agent   # 重启"
echo "  sudo systemctl stop fishtty-agent      # 停止"
echo "  sudo systemctl disable fishtty-agent   # 禁止开机自启"
