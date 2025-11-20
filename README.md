# Relify - 跨平台消息聚合系统

## 架构概述

Relify 采用**六边形架构**（Hexagonal Architecture），实现了无中心对等的消息路由系统。

### 核心组件

```text
┌─────────────────────────────────────────┐
│          驱动层 (Adapters)              │
│  ┌──────────┐  ┌──────────┐  ┌────────┐│
│  │ Telegram │  │ Discord  │  │ Matrix ││
│  └────┬─────┘  └────┬─────┘  └───┬────┘│
└───────┼─────────────┼─────────────┼─────┘
        │             │             │
        │  入站消息   │             │
        ▼             ▼             ▼
┌─────────────────────────────────────────┐
│         核心层 (Core)                    │
│  ┌──────────────────────────────────┐   │
│  │  Router (路由引擎)                │   │
│  │  - ID 翻译                        │   │
│  │  - 能力感知路由                   │   │
│  │  - 消息指纹生成                   │   │
│  └──────────────────────────────────┘   │
│                                          │
│  ┌──────────────┐  ┌─────────────────┐  │
│  │ RouteStore   │  │ MessageMapStore │  │
│  │ (路由绑定)   │  │ (ID 映射)       │  │
│  └──────────────┘  └─────────────────┘  │
└─────────────────────────────────────────┘
        │             │             │
        │  出站消息   │             │
        ▼             ▼             ▼
┌─────────────────────────────────────────┐
│          驱动层 (Adapters)              │
│  ┌──────────┐  ┌──────────┐  ┌────────┐│
│  │ Telegram │  │ Discord  │  │ Matrix ││
│  └──────────┘  └──────────┘  └────────┘│
└─────────────────────────────────────────┘
```

## 项目结构（标准 Go 布局）

```text
Relify/
├── cmd/                      # 主应用程序入口
│   └── relify/
│       └── main.go           # 主程序
├── internal/                 # 私有应用和库代码
│   ├── core/                 # 核心层
│   │   └── core.go           # 核心入口
│   ├── model/                # 数据模型
│   │   ├── message.go        # 消息模型
│   │   └── route.go          # 路由模型
│   ├── storage/              # 持久化存储
│   │   ├── message_map.go    # ID 映射存储
│   │   └── route_store.go    # 路由绑定存储
│   ├── router/               # 路由引擎
│   │   ├── router.go         # 核心路由逻辑
│   │   └── router_test.go    # 路由测试
│   └── driver/               # 驱动接口
│       └── interface.go      # 统一驱动接口
├── pkg/                      # 公共库代码（待添加）
├── drivers/                  # 平台驱动实现（待实现）
│   ├── telegram/
│   ├── discord/
│   └── matrix/
├── bin/                      # 编译输出
│   └── relify.exe
├── go.mod
├── go.sum
├── Relify.md                 # 产品需求文档
└── README.md
```

## 核心特性

### 1. 统一消息模型

基于 Matrix 标准裁剪的消息结构，支持：

- 文本、图片、文件、音频、视频
- 富文本（HTML/Markdown）
- 消息引用/回复
- 消息编辑/撤回

### 2. ID 映射与翻译

- **自动 ID 翻译**：入站消息的引用 ID 自动翻译为目标平台的消息 ID
- **TTL 管理**：映射关系保留 48 小时，过期自动清理
- **多对多映射**：一条源消息可映射到多个目标平台

### 3. 能力感知路由

驱动声明自身能力（Webhook、编辑、回复等），路由引擎根据能力决定：

- 是否启用回复翻译
- 是否同步编辑/撤回
- 消息降级策略

### 4. 全链路异步

- **即发即弃**：核心层收到消息后立即返回 HTTP 200
- **异步分发**：所有出站消息在独立 Goroutine 中执行
- **异步持久化**：ID 映射写入数据库不阻塞主流程

### 5. 内存直通

- 路由绑定关系启动时全量加载到内存
- 消息分发基于内存查找，无数据库查询
- 无消息队列，直接函数调用

### 6. 消息指纹

- **简单高效**：使用 `driver:room:msgid:timestamp` 格式拼接
- **无加密开销**：不需要哈希计算，性能更优
- **唯一性保证**：纳秒级时间戳确保全局唯一

## 快速开始

### 编译

```bash
go build -o bin/relify.exe ./cmd/relify
```

### 运行测试

```bash
# 测试路由引擎
go test ./internal/router -v

# 测试所有模块
go test ./... -v
```

### 初始化核心层

```go
package main

import (
    "context"
    "github.com/YisRime/relify/internal/core"
)

func main() {
    // 创建核心实例
    coreInstance, err := core.NewCore(&core.Config{
        DatabasePath: "./relify.db",
    })
    if err != nil {
        panic(err)
    }

    // 注册驱动（待实现）
    // telegramDriver := telegram.NewDriver(telegramConfig)
    // coreInstance.RegisterDriver(telegramDriver)

    // 启动核心层
    ctx := context.Background()
    if err := coreInstance.Start(ctx); err != nil {
        panic(err)
    }
}
```

## 技术栈

- **语言**：Go 1.25.4
- **数据库**：SQLite (WAL 模式)
- **并发模型**：Goroutine + Channel
- **架构模式**：六边形架构 (Hexagonal Architecture)
- **项目布局**：标准 Go 项目结构

## 开发进度

- [x] 核心消息模型
- [x] ID 映射系统
- [x] 路由引擎
- [x] 驱动接口定义
- [x] 单元测试
- [ ] 驱动实现（Telegram、Discord、Matrix）
- [ ] 配置管理（YAML）
- [ ] 日志系统
- [ ] HTTP API
- [ ] 媒体代理

## 下一步开发

1. **驱动实现**：
   - Telegram 驱动
   - Discord 驱动
   - Matrix 驱动

2. **配置管理**：
   - YAML 配置文件解析
   - 热重载支持

3. **日志系统**：
   - 结构化日志（JSON）
   - 分级日志输出

4. **HTTP API**：
   - 配置管理接口
   - 健康检查接口
   - 统计信息接口

5. **媒体代理**：
   - 私有链接代理
   - 流式转发
   - 缓存优化

## 许可证

MIT License
