# Relify

<div align="center">

![License](https://img.shields.io/github/license/YisRime/Relify)
![Go Version](https://img.shields.io/github/go-mod/go-version/YisRime/Relify)
![Build Status](https://img.shields.io/github/actions/workflow/status/YisRime/Relify/main.yml?branch=main)
![Release](https://img.shields.io/github/v/release/YisRime/Relify)

[English](./readme_en.md) | 简体中文

轻量级**跨平台**消息中继 · 不同平台用户**无缝沟通**

如果这个项目对您有帮助，请给我一个 ⭐️ Star！

**QQ 群**: [855571375](https://qm.qq.com/q/PdLMx9Jowq) - 中文用户交流、问题反馈

</div>

---

## 📖 简介

Relify 是一个高性能的跨平台消息桥接服务，专为实现 **Matrix**、**QQ** 等不同即时通讯平台之间的消息互通而设计。

### 🎯 核心优势

- **极致性能** - 基于 Go 语言开发，充分利用协程并发优势，单实例轻松处理数万条消息
- **零配置启动** - 智能识别消息流向，自动建立房间映射关系，开箱即用
- **完整消息支持** - 不仅传递文本，还完整支持图片、视频、音频、文件等多媒体内容
- **高级特性同步** - 消息编辑、撤回、回复引用等高级功能跨平台完美同步
- **架构清晰** - 简洁的适配器接口设计，轻松扩展支持新的聊天平台
- **可靠稳定** - SQLite 持久化存储，消息映射关系不丢失，服务重启后自动恢复
- **数据安全** - 所有消息处理本地化，不依赖第三方云服务

### 💡 适用场景

- **开源社区桥接** - 连接 Matrix 开源社区与 QQ/微信等国内主流平台
- **团队协作** - 统一不同部门使用的多种聊天工具
- **机器人集成** - 构建跨平台的智能客服或通知系统
- **多平台管理** - 在一个客户端管理多个平台的消息

## ✨ 特性

### 基础消息类型

- ✅ **文本消息** - 支持 Markdown 格式、表情符号
- ✅ **图片** - 自动格式转换和压缩，支持动图
- ✅ **视频** - 跨平台视频传输，格式兼容处理
- ✅ **音频** - 语音消息完整同步
- ✅ **文件** - 任意类型文件传输

### 高级消息特性

- ✅ **消息编辑** - 编辑后自动同步到所有桥接平台
- ✅ **消息撤回** - 跨平台撤回，保持操作一致性
- ✅ **回复引用** - 保留消息上下文，支持引用链追溯
- ✅ **@提及** - 跨平台用户提及和通知
- ✅ **转发** - 消息转发时保留原始发送者信息

### 元数据同步

- ✅ **发送者信息** - 昵称、头像自动同步
- ✅ **群组信息** - 群名称、群公告同步
- ✅ **时间戳** - 消息时间精确到毫秒
- ✅ **已读状态** - 消息已读状态跨平台同步

## 📦 快速开始

### 前置要求

- **Matrix 服务器**（如 [Synapse](https://github.com/matrix-org/synapse)）
- **QQ OneBot 实现**（如 [Lagrange](https://github.com/LagrangeDev/Lagrange.Core) 或 [NapCat](https://github.com/NapNeko/NapCatQQ)）

### 安装

从 [Releases](https://github.com/YisRime/Relify/releases) 下载适合您系统的版本，首次运行会在 `./data` 目录下自动生成默认配置文件 `config.yaml`。

### 配置

编辑 `config.yaml`，根据您的实际情况调整配置：

```yaml
# 基础配置
log_level: "info"       # 日志级别: debug | info | warn | error
mode: "hub"             # 运行模式: hub（星型拓扑）| mesh（网状拓扑）
hub: "matrix"           # 中心平台 ID（hub 模式必填）
retent_day: 30          # 消息映射关系保留天数

platforms:
  # Matrix 平台配置
  matrix:
    driver: "matrix"    # 驱动类型
    enabled: true       # 是否启用
    config:
      # Matrix 服务器地址（Synapse 默认 8448 端口）
      server_url: "http://localhost:8448"
      domain: "your.domain"              # 您的 Matrix 域名
      server_domain: ""                  # 服务器实际运行的域名（可选）

      # AppService 配置
      appservice:
        id: "relify"
        token: "your_generated_token"
        namespace: "relify_"
        listen: "http://localhost:6168"
      
      # 可选：自动邀请用户到新创建的房间
      auto_invite: "@admin:your.domain"

  # QQ 平台配置
  qq:
    driver: "qq"
    enabled: true
    config:
      protocol: "ws"                      # 协议: ws | wss | http
      url: "ws://localhost:3001"          # OneBot 实现地址
      secret: ""                          # 如果配置了 access_token 需填写
      group: ""                           # Mix 模式下的默认群号
```

#### 注册 AppService（仅 Matrix）

创建 `registration.yaml` 文件：

```yaml
id: relify
url: http://localhost:6168
as_token: your_generated_token_here      # 与 config.yaml 中的 token 保持一致
hs_token: your_generated_token_here      # 与 config.yaml 中的 token 保持一致
sender_localpart: relify                 # 此项为固定要求
namespaces:
  users:
    - exclusive: true
      regex: "@relify_.*:your.domain"    # 与 config.yaml 中的 namespace 保持一致
  aliases:
    - exclusive: true
      regex: "#relify_.*:your.domain"    # 与 config.yaml 中的 namespace 保持一致
  rooms: []
```

然后在 Synapse 中进行注册，修改 `homeserver.yaml`：

```yaml
app_service_config_files:
  - /path/to/registration.yaml
```

或是使用 Conduit，使用管理员命令进行注册：

```bash
!admin appservices register ```yaml```
```

### 启动

```bash
# 前台运行（测试）
./relify-linux-amd64

# 后台运行（生产）
nohup ./relify-linux-amd64 > relify.log 2>&1 &

# 使用 systemd（推荐）
sudo nano /etc/systemd/system/relify.service
```

## 🙏 致谢

Relify 的诞生离不开以下优秀的开源项目：

- [Matrix](https://matrix.org/) - 开放的去中心化通讯协议标准
- [mautrix-go](https://github.com/mautrix/go) - 强大的 Matrix Go SDK
- [Lagrange](https://github.com/LagrangeDev/Lagrange.Core) - 优秀的 NTQQ 协议实现
- [NapCat](https://github.com/NapNeko/NapCatQQ) - 现代化的 OneBot 实现

## 📄 开源协议

本项目采用 [License](./LICENSE) 开源协议。

[![Star History Chart](https://api.star-history.com/svg?repos=YisRime/Relify&type=Date)](https://star-history.com/#YisRime/Relify&Date)
