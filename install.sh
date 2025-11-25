#!/bin/bash
set -e

# --- 全局配置 ---
REPO="YisRime/Relify"
APP_NAME="relify"
INSTALL_DIR="/opt/${APP_NAME}"
BIN_LINK="/usr/local/bin/${APP_NAME}"
SERVICE_FILE="/etc/systemd/system/${APP_NAME}.service"

# --- 样式定义 ---
GREEN="\033[32m"
RED="\033[31m"
YELLOW="\033[33m"
RESET="\033[0m"

log_info() { echo -e "${GREEN}[INFO]${RESET} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${RESET} $1"; }
log_err()  { echo -e "${RED}[ERROR]${RESET} $1"; }

# --- 1. 环境检测 ---
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
    Linux)  OS_TYPE="linux" ;;
    Darwin) OS_TYPE="darwin" ;;
    *) log_err "不支持的操作系统: $OS"; exit 1 ;;
esac

case "$ARCH" in
    x86_64) ARCH_TYPE="amd64" ;;
    arm64|aarch64) ARCH_TYPE="arm64" ;;
    *) log_err "不支持的架构: $ARCH"; exit 1 ;;
esac

# --- 2. 获取版本 ---
VERSION=$1
if [ -z "$VERSION" ]; then
    log_info "正在查询最新版本..."
    # 使用 GitHub API 获取最新 Release Tag
    LATEST_URL="https://api.github.com/repos/$REPO/releases/latest"
    VERSION=$(curl -s $LATEST_URL | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
fi

if [ -z "$VERSION" ]; then
    log_err "无法获取版本信息，请检查网络或指定版本号。"
    exit 1
fi

# 构建文件名 (需与 build.go 生成的一致)
FILE_NAME="${APP_NAME}-${OS_TYPE}-${ARCH_TYPE}"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$VERSION/$FILE_NAME"

# --- 3. 下载与安装 ---
log_info "准备安装版本: $VERSION ($OS_TYPE/$ARCH_TYPE)"

# 创建临时文件
TMP_FILE="/tmp/$FILE_NAME"

log_info "正在下载..."
curl -L -o "$TMP_FILE" --fail "$DOWNLOAD_URL"
chmod +x "$TMP_FILE"

log_info "部署文件到 $INSTALL_DIR ..."
# 确保目录存在
sudo mkdir -p "$INSTALL_DIR"

# 移动二进制文件
sudo mv "$TMP_FILE" "$INSTALL_DIR/$APP_NAME"

# 创建软链接 (方便命令行直接调用)
log_info "创建软链接 $BIN_LINK ..."
sudo ln -sf "$INSTALL_DIR/$APP_NAME" "$BIN_LINK"

# --- 4. Systemd 配置 (仅 Linux) ---
if [ "$OS_TYPE" == "linux" ] && command -v systemctl >/dev/null 2>&1; then
    log_info "检测到 Systemd，正在配置服务..."

    # 写入服务文件
    sudo bash -c "cat > $SERVICE_FILE" <<EOF
[Unit]
Description=Relify Service
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/$APP_NAME
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF

    log_info "启动服务..."
    sudo systemctl daemon-reload
    sudo systemctl enable "$APP_NAME"
    sudo systemctl restart "$APP_NAME"
    
    log_info "服务状态检查:"
    sudo systemctl status "$APP_NAME" --no-pager | head -n 3
else
    log_warn "非 Linux Systemd 环境，跳过服务配置。"
fi

echo
log_info "安装成功！"
log_info "程序路径: $INSTALL_DIR/$APP_NAME"
if [ "$OS_TYPE" == "linux" ]; then
    log_info "服务管理: sudo systemctl [start|stop|status] $APP_NAME"
fi