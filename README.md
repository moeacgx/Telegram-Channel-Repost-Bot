# 🤖 Telegram Channel Repost Bot

<div align="center">

![Go](https://img.shields.io/badge/Go-1.19+-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![Telegram](https://img.shields.io/badge/Telegram-Bot-26A5E4?style=for-the-badge&logo=telegram&logoColor=white)
![SQLite](https://img.shields.io/badge/SQLite-003B57?style=for-the-badge&logo=sqlite&logoColor=white)
![License](https://img.shields.io/badge/License-MIT-green?style=for-the-badge)

**一个功能强大的Telegram频道管理机器人，支持定时重发、无引用转发、批量管理等高级功能**

[功能特性](#-功能特性) • [快速开始](#-快速开始) • [使用指南](#-使用指南) • [配置说明](#-配置说明)

###  联系我们
- 💬 **站长交流群**: [https://t.me/vpsbbq](https://t.me/vpsbbq)
- 📦 **站长仓库**: [https://t.me/zhanzhangck](https://t.me/zhanzhangck)

</div>
请等待重构！目前某些功能不符合逻辑！！
---

## ✨ 功能特性

### 🎯 核心功能
- 🏗️ **频道组管理** - 创建和管理频道组，每个频道组可以包含多个频道
- ⏰ **定时重发** - 自动定时重发消息，智能删除上次发送的消息避免重复
- 📤 **手动推送** - 支持手动推送消息到指定频道组
- 🗑️ **消息删除** - 支持删除整个频道组的已发送消息
- ⚡ **立即重发** - 支持手动触发立即重发定时内容

### 🚀 高级功能
- 📋 **无引用转发** - 转发消息时不显示原始来源，保持内容原创性
- 🔗 **超链接保留** - 完美保留消息中的超链接和格式
- 📱 **媒体组支持** - 完整转发媒体组（图片、视频组合）
- 📊 **批量添加频道** - 支持一行一个频道ID的批量添加
- 🎨 **消息预览** - 发送前预览消息效果
- 🔘 **跳转按钮** - 为消息添加自定义跳转按钮
- 📈 **发送统计** - 查看发送历史、状态和失败原因
- 🔄 **重试机制** - 智能重试失败的发送操作
- 🎛️ **Bot交互** - 所有操作通过友好的按钮界面完成

### 🛡️ 安全特性
- 🔒 **URL预览关闭** - 自动关闭转发消息的URL预览
- 🎭 **无引用转发** - 保护原始消息来源
- 📝 **完整格式保留** - 保持原始消息的所有格式和链接

## 📁 项目结构

```
📦 tg-channel-repost-bot
├── 📂 cmd/
│   └── 📂 server/          # 🚀 主程序入口
├── 📂 internal/
│   ├── 📂 bot/            # 🤖 Telegram Bot 处理逻辑
│   ├── 📂 database/       # 🗄️ 数据库连接和操作
│   ├── 📂 models/         # 📋 数据模型
│   ├── 📂 services/       # ⚙️ 业务逻辑服务
│   ├── 📂 handlers/       # 🔧 请求处理器
│   └── 📂 scheduler/      # ⏰ 定时任务调度器
├── 📂 pkg/
│   └── 📂 config/         # ⚙️ 配置管理
├── 📂 configs/            # 📝 配置文件
├── 📂 migrations/         # 🔄 数据库迁移
└── 📂 docs/              # 📚 文档
```

## 🚀 快速开始

### 📋 前置要求
- Go 1.19+
- SQLite3
- Telegram Bot Token

### 1️⃣ 克隆项目
```bash
git clone https://github.com/moeacgx/tg-channel-repost-bot.git
cd tg-channel-repost-bot
```

### 2️⃣ 安装依赖
```bash
go mod tidy
```

### 3️⃣ 配置Bot Token
编辑 `configs/config.yaml` 文件，设置你的Telegram Bot Token：

```yaml
telegram:
  bot_token: "YOUR_BOT_TOKEN_HERE"

database:
  dsn: "bot.db"

scheduler:
  check_interval: 60
  max_workers: 50
  retry_attempts: 3
  retry_interval: 300
```

### 4️⃣ 运行程序
```bash
# 开发模式
go run cmd/server/main.go

# 或者编译后运行
go build -o bot cmd/server/main.go
./bot
```

### 5️⃣ 开始使用
1. 在Telegram中找到你的Bot
2. 发送 `/start` 命令
3. 开始配置频道组和消息！

## ⚙️ 配置说明

### 🔧 主要配置项

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `telegram.bot_token` | Telegram Bot Token（必需） | - |
| `database.dsn` | 数据库连接字符串 | `bot.db` |
| `scheduler.check_interval` | 调度器检查间隔（秒） | `60` |
| `scheduler.max_workers` | 最大工作线程数 | `50` |
| `scheduler.retry_attempts` | 重试次数 | `3` |
| `scheduler.retry_interval` | 重试间隔（秒） | `300` |

## 📖 使用指南

### 🎯 基础操作

#### 1️⃣ 启动Bot
运行程序后，在Telegram中找到你的Bot并发送 `/start` 命令。

#### 2️⃣ 创建频道组
1. 点击 "📋 管理频道组"
2. 点击 "➕ 创建新组"
3. 输入频道组名称和描述
4. 设置发送频率（小时）

#### 3️⃣ 添加频道
1. 选择已创建的频道组
2. 点击 "➕ 添加频道"
3. 输入频道ID（支持批量添加，一行一个）：
   ```
   @channel1
   @channel2
   -1001234567890
   -1009876543210
   ```

#### 4️⃣ 设置消息模板
1. 点击 "📤 发送消息" → "📢 推送消息"
2. 选择频道组
3. 发送或转发消息给Bot
4. 添加跳转按钮（可选）
5. 预览并确认发送

### 🚀 高级功能

#### 📋 无引用转发
1. 点击 "📤 发送消息" → "📤 无引用转发"
2. 转发任意消息给Bot（支持媒体组）
3. 选择目标频道组
4. 消息将以原创形式转发，保留所有格式和链接

#### ⚡ 立即重发定时内容
1. 点击 "📤 发送消息" → "🔄 立即重发定时内容"
2. 选择频道组
3. 立即发送该组的定时消息模板

#### 🗑️ 删除消息
1. 点击 "📤 发送消息" → "🗑️ 删除消息"
2. 选择频道组
3. 删除该组在所有频道的最新消息

### 📊 管理功能

#### 📈 查看记录
- 查看发送历史
- 查看失败原因
- 统计发送成功率

#### ⚙️ 设置
- 配置重试参数
- 调整发送频率
- 管理频道状态

## 🗄️ 数据库结构

项目使用SQLite数据库，包含以下主要表：

| 表名 | 说明 | 主要字段 |
|------|------|----------|
| `channel_groups` | 频道组信息 | id, name, description, frequency |
| `channels` | 频道信息 | id, channel_id, group_id, is_active |
| `message_templates` | 消息模板 | id, group_id, content, message_type |
| `send_records` | 发送记录 | id, group_id, status, sent_at |
| `retry_configs` | 重试配置 | id, max_attempts, retry_interval |

## 🔧 技术栈

### 🛠️ 核心技术
- **语言**: Go 1.19+
- **数据库**: SQLite3
- **Bot框架**: go-telegram-bot-api/v5
- **配置**: YAML
- **调度**: 自定义调度器

### 📦 主要依赖
```go
github.com/go-telegram-bot-api/telegram-bot-api/v5
github.com/mattn/go-sqlite3
gopkg.in/yaml.v2
```

## 🚀 部署说明

### 🐳 Docker部署
```bash
# 构建镜像
docker build -t tg-repost-bot .

# 运行容器
docker run -d \
  --name tg-repost-bot \
  -v $(pwd)/configs:/app/configs \
  -v $(pwd)/data:/app/data \
  tg-repost-bot
```

### 🖥️ 系统服务
```bash
# 创建systemd服务
sudo cp tg-repost-bot.service /etc/systemd/system/
sudo systemctl enable tg-repost-bot
sudo systemctl start tg-repost-bot
```

## 🤝 贡献指南

### 📋 开发环境设置
1. 安装Go 1.19或更高版本
2. 安装SQLite3
3. Fork并克隆项目
4. 安装依赖：`go mod tidy`
5. 配置Bot Token
6. 运行测试：`go test ./...`

### 🔄 贡献流程
1. 🍴 Fork项目
2. 🌿 创建功能分支：`git checkout -b feature/amazing-feature`
3. 💾 提交更改：`git commit -m 'Add amazing feature'`
4. 📤 推送分支：`git push origin feature/amazing-feature`
5. 🔀 创建Pull Request

### 📝 代码规范
- 遵循Go官方代码规范
- 添加必要的注释和文档
- 编写单元测试
- 确保所有测试通过

## 📄 许可证

本项目采用MIT许可证。详见 [LICENSE](LICENSE) 文件。

## 🆘 支持与反馈

### 🐛 问题报告
如果您发现bug或有功能建议，请：
1. 在GitHub上创建Issue
2. 加入我们的交流群讨论

### 💬 联系方式
- 💬 **站长交流群**: [https://t.me/vpsbbq](https://t.me/vpsbbq)
- 📦 **站长仓库**: [https://t.me/zhanzhangck](https://t.me/zhanzhangck)
- 🐙 **GitHub Issues**: [创建Issue](https://github.com/moeacgx/tg-channel-repost-bot/issues)

---

<div align="center">

**⭐ 如果这个项目对您有帮助，请给我们一个Star！**

Made with ❤️ by the community

</div>
