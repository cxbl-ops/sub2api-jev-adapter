# 安装 JEV Adapter

## 1. 信任插件签名

插件包已经使用 `cxbl-jev-adapter-v1` Ed25519 密钥签名。先把
`dist/trusted-publisher.yaml` 中的配置合并到 Sub2API 的 `config.yaml`：

```yaml
plugins:
  allow_unsigned: false
  trusted_publishers:
    cxbl-jev-adapter-v1: "TftWbNgbPfM/fJ+o2q2AtbAdddew8ePPj+b5UQ4QR1E="
```

若 `config.yaml` 已有 `plugins:`，只合并 `trusted_publishers` 项，不要重复创建顶层键。
重启 Sub2API，让可信发布者配置生效。

## 2. 上传并配置

1. 打开“插件管理”；
2. 上传 `dist/sub2api-jev-adapter-0.1.0.s2plugin`；
3. 打开插件配置，填写自己的 JEV Key；
4. 保存并测试；测试会产生一次极小额 JEV 调用；
5. 启用插件。首次建议使用小灰度，确认普通 OpenAI OAuth 请求仍正常后再调到 100%。

Sub2API 同时只能启用一个 `openai.oauth.outbound_transport.v1` 插件；若已有同能力插件，
需先停用。

## 3. 调用

```bash
curl https://your-sub2api.example.com/v1/responses \
  -H "Authorization: Bearer sk-你的Sub2API-Key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "jev-latest",
    "stream": false,
    "input": "JEV structured decision",
    "state": "Customer: I was charged twice and nobody replied for 3 days.",
    "questions": {
      "route": {
        "type": "choice",
        "instructions": "Where should this go?",
        "criteria": {"billing":"money", "bug":"broken", "account":"login"}
      },
      "escalate": {
        "type": "noul",
        "instructions": "Escalate to a human now?"
      }
    }
  }'
```

返回值遵循 OpenAI Responses 格式。JEV 原始结构化结果可从以下任一位置读取：

- 顶层 `jev`；
- `output[0].content[0].text`（JSON 字符串）。

## 4. 宿主侧模型配置

插件协议不能自动修改 Sub2API 的渠道、分组、模型广场或价格。请确保调用所用分组允许
`jev-latest`，并为其配置符合您售卖策略的价格。JEV 上游返回的 `cost_usd` 只作为结果
元数据；Sub2API 仍按您在宿主中配置的模型价格计费。

## 5. 安全事项

- 插件包不含 JEV Key；Key 由 Sub2API 的插件配置加密保存；
- 任何曾经公开、共享或写入日志的 JEV Key 都应立即撤销并轮换；
- `publisher.private` 仅在本地 `build/keys/`，未打入插件包；
- 可在上传前运行 `cd dist && sha256sum -c SHA256SUMS` 验证包完整性。
