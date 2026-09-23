# Sub2API JEV Adapter

[![Release](https://img.shields.io/github/v/release/cxbl-ops/sub2api-jev-adapter)](https://github.com/cxbl-ops/sub2api-jev-adapter/releases)
[![CI](https://github.com/cxbl-ops/sub2api-jev-adapter/actions/workflows/ci.yml/badge.svg)](https://github.com/cxbl-ops/sub2api-jev-adapter/actions/workflows/ci.yml)
[![License: LGPL-3.0](https://img.shields.io/badge/License-LGPL--3.0-blue.svg)](LICENSE)

一个面向 [Sub2API](https://github.com/Wei-Shaw/sub2api) 的 JEV Decision API 适配插件。

插件把 `jev-*` 模型请求转换为 [JEV](https://jevtypesafeai.com/docs) 原生的
`state + questions` 决策请求，并将结果包装为 OpenAI Responses 格式。非 JEV 请求会按
原始地址、请求头和响应流透明转发。

> 这是社区项目，与 Sub2API、JEV 或 TypeSafe AI 官方无隶属关系。

## 功能

- 支持 `jev-latest` 和固定版本的 `jev-*` 模型；
- 支持 JEV 的 `noul`、`choice`、`score` 三种问题类型；
- 支持字符串、对象和数组形式的 `state`；
- 输出标准 OpenAI Responses 响应，并额外提供顶层 `jev` 结果；
- 支持 `stream:false` 和兼容式 `stream:true`；
- 通过 Sub2API 插件配置加密保存 JEV API Key；
- 不向下游暴露 JEV 账户剩余余额；
- 非 JEV OpenAI OAuth 请求透明转发；
- 支持 HTTP、HTTPS 和 SOCKS5 账号代理。

## 兼容性

| 项目 | 支持情况 |
|---|---|
| Sub2API | `>= 0.2.7, < 0.3.0` |
| 插件协议 | v1 |
| 平台 | Linux amd64 |
| 插件能力 | `openai.oauth.outbound_transport.v1` |

受 Sub2API 当前插件协议限制，必须至少有一个可调度的 OpenAI OAuth 账号作为插件调度入口。
JEV 请求命中插件后不会发给 OpenAI，OpenAI OAuth 账号只承担宿主调度入口的作用。

## 安装

### 1. 下载 Release

从 [Releases](https://github.com/cxbl-ops/sub2api-jev-adapter/releases) 下载：

- `sub2api-jev-adapter-0.1.0.s2plugin`
- `SHA256SUMS`
- `trusted-publisher.yaml`

### 2. 信任发布者签名

将以下内容合并到 Sub2API 的 `config.yaml`：

```yaml
plugins:
  allow_unsigned: false
  trusted_publishers:
    cxbl-jev-adapter-v1: "TftWbNgbPfM/fJ+o2q2AtbAdddew8ePPj+b5UQ4QR1E="
```

如果配置中已有 `plugins:`，只合并 `trusted_publishers`，不要创建重复的顶层键。修改后重启
Sub2API。

### 3. 上传与启用

1. 打开 Sub2API 管理后台的“插件管理”；
2. 上传 `.s2plugin` 文件；
3. 打开插件配置，填写自己的 JEV API Key；
4. 保存并测试；
5. 测试成功后启用插件；
6. 在专用分组中允许 `jev-latest`，并配置模型定价。

完整说明见 [安装指南](docs/INSTALL.md)。

## 快速调用

```bash
curl https://your-sub2api.example.com/v1/responses \
  -H "Authorization: Bearer sk-your-sub2api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "jev-latest",
    "stream": false,
    "input": "分析客服工单",
    "state": {
      "subject": "重复扣款",
      "message": "同一订单被扣款两次，三天没有回复"
    },
    "questions": {
      "route": {
        "type": "choice",
        "instructions": "应该把工单交给哪个部门？",
        "criteria": {
          "billing": "扣款、退款和发票问题",
          "technical": "产品故障或功能异常",
          "account": "登录、账号和权限问题"
        }
      },
      "escalate": {
        "type": "noul",
        "instructions": "是否应该立即转交人工？"
      }
    }
  }'
```

推荐从响应的 `jev.answers` 读取结果：

```json
{
  "jev": {
    "model": "jev-1.13.0",
    "answers": {
      "route": {
        "type": "choice",
        "choice": "billing",
        "confidence": 0.99
      },
      "escalate": {
        "type": "noul",
        "noul": 0.94
      }
    }
  }
}
```

标准 Responses 客户端也可以解析 `output[0].content[0].text`，其内容是相同结果的 JSON
字符串。

## 工作方式

```text
客户端
  │  POST /v1/responses, model=jev-*
  ▼
Sub2API 调度 OpenAI OAuth 账号
  │  openai.oauth.outbound_transport.v1
  ▼
JEV Adapter
  ├─ jev-*：转换并请求 JEV /api/v1/decide
  └─ 其他模型：透明转发原始 OpenAI 请求
```

插件能力会接收命中灰度的 OpenAI OAuth 出站流量，因此请只安装可信构建，并先小流量验证
透明转发行为。

## 从源码构建

要求：

- Go 1.27 或更高版本；
- Linux amd64 构建环境。

```bash
git clone https://github.com/cxbl-ops/sub2api-jev-adapter.git
cd sub2api-jev-adapter
go test ./... -count=1
./build.sh
```

首次运行 `build.sh` 会在本机 `build/keys/` 生成一对 Ed25519 发布者密钥。私钥和所有
构建产物均被 `.gitignore` 排除，不会提交到 Git。

构建结果位于：

```text
dist/sub2api-jev-adapter-0.1.0.s2plugin
dist/publisher-public-key.txt
dist/trusted-publisher.yaml
dist/SHA256SUMS
```

验证包签名、文件哈希、插件进程握手和一次本地端到端转发：

```bash
go run ./tools/verify-package \
  -package dist/sub2api-jev-adapter-0.1.0.s2plugin \
  -public-key dist/publisher-public-key.txt
```

## 项目结构

```text
cmd/jev-adapter/       插件进程入口
internal/adapter/      配置、JEV 转换与透明传输
sdk/pluginapi/v1/      Sub2API v0.2.7 插件协议源码
tools/keygen/          Ed25519 密钥生成器
tools/packager/        .s2plugin 打包与签名工具
tools/verify-package/  离线验签及进程级验证工具
ui/                    沙箱配置页面
docs/                  用户、安装和开发者文档
```

## 使用限制

- JEV 是结构化决策模型，不是普通聊天模型；
- 请使用 `/v1/responses`，并提供 `state` 和非空 `questions`；
- 当前不支持通过 `/v1/chat/completions` 自由聊天；
- JEV 上游本身不是流式服务，`stream:true` 只提供兼容式一次性事件输出；
- Sub2API 同时只能启用一个相同的 OpenAI OAuth 出站插件能力；
- 模型白名单、模型广场和价格需要由 Sub2API 管理员配置。

## 安全

- 仓库和 Release 不包含任何 JEV API Key、管理员 Key、用户数据或服务器配置；
- JEV API Key 仅通过插件配置写入，并由 Sub2API 加密保存；
- 插件不会记录完整请求体、凭据或敏感上游响应；
- `credits_remaining_usd` 会在响应下发前移除；
- 发布包使用 Ed25519 签名，安装前应核对 Release 校验和。

如发现安全问题，请通过 GitHub Security Advisory 私下报告，不要在公开 Issue 中提交密钥、
请求内容或服务器信息。

## 文档

- [面向用户的 JEV 使用指南](docs/JEV-用户使用指南.md)
- [开发者接入文档](docs/JEV-开发者接入文档.md)
- [插件安装指南](docs/INSTALL.md)

## 许可证与致谢

本项目采用 [LGPL-3.0](LICENSE) 许可证。

`sdk/pluginapi/v1` 源自 Sub2API v0.2.7 的公开插件协议，版权及许可信息见
[NOTICE.md](NOTICE.md)。感谢 Sub2API 和 JEV/TypeSafe AI 项目提供的协议与服务。
