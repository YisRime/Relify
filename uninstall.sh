#!/bin/bash

# --- 全局配置 ---
APP_NAME="relify"
INSTALL_DIR="/opt/${APP_NAME}"
BIN_LINK="/usr/local/bin/${APP_NAME}"
SERVICE_FILE="/etc/systemd/system/${APP_NAME}.service"

# --- 样式定义 ---
GREEN="\033[32m"
RED="\033[31m"
RESET="\033[0m"

log_info() { echo -e "${GREEN}[INFO]${RESET} $1"; }
log_err()  { echo -e "${RED}[ERROR]${RESET} $1"; }

log_info "开始卸载 $APP_NAME ..."

# 1. 停止并移除服务
if command -v systemctl >/dev/null 2>&1; then
    if systemctl list-units --full -all | grep -Fq "$APP_NAME.service"; then
        log_info "停止并禁用 Systemd 服务..."
        sudo systemctl stop "$APP_NAME"
        sudo systemctl disable "$APP_NAME"
        
        if [ -f "$SERVICE_FILE" ]; then
            log_info "删除服务文件: $SERVICE_FILE"
            sudo rm "$SERVICE_FILE"
            sudo systemctl daemon-reload
        fi
    fi
fi

# 2. 删除软链接
if [ -L "$BIN_LINK" ]; then
    log_info "删除命令链接: $BIN_LINK"
    sudo rm "$BIN_LINK"
fi

# 3. 删除安装目录
if [ -d "$INSTALL_DIR" ]; then
    log_info "删除安装目录: $INSTALL_DIR"
    sudo rm -rf "$INSTALL_DIR"
else
    log_err "未找到安装目录，可能已卸载。"
fi

echo
log_info "卸载完成！"