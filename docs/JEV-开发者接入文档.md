# JEV 模型使用文档

本文说明如何通过 Sub2API 的 JEV Adapter 调用 JEV Decision API。

## 一、JEV 是什么

JEV 是结构化判断模型，不是普通聊天或文本生成模型。

它接收两部分内容：

- `state`：需要分析的事实、数据或上下文；
- `questions`：需要针对这些事实作出的判断。

JEV 返回类型固定的结构化结果，适合分类、路由、风险判断、评分、审核和自动决策。

JEV 不适合以下用途：

- 自由聊天；
- 写文章或生成长文本；
- 普通知识问答；
- 直接在只支持 `/v1/chat/completions` 的聊天页面中使用。

## 二、调用信息

### 请求地址

```text
POST https://your-sub2api.example.com/v1/responses
```

### 请求头

```text
Authorization: Bearer sk-你的Sub2API用户Key
Content-Type: application/json
```

这里使用的是绑定到 JEV 分组的 Sub2API 用户 Key：

- 不要使用管理员 Key；
- 不要在请求中填写 JEV 上游 Key；
- JEV 上游 Key 只保存在服务器的插件配置中。

## 三、请求字段

### `model`

指定 JEV 模型。推荐使用：

```json
"model": "jev-latest"
```

也可以使用固定版本：

```json
"model": "jev-1.13.0"
```

- `jev-latest`：自动使用当前最新版本；
- 固定版本：行为更稳定，适合需要固定阈值的生产环境。

### `stream`

是否使用 OpenAI Responses 流式返回。

```json
"stream": false
```

建议使用 `false`。JEV 上游本身不是流式模型；即使设置为 `true`，也需要等待 JEV 完成判断后才会一次性返回结果。

### `input`

`input` 是用于兼容 Sub2API Responses 接口的任务描述，例如：

```json
"input": "分析客服工单并给出结构化判断"
```

当请求包含 `state` 时，插件不会把 `input` 作为 JEV 的分析资料。

如果没有提供 `state`，插件会尝试把 `input` 作为 `state`。为了让请求含义清晰，建议始终显式填写 `state`。

### `state`

`state` 是 JEV 实际需要分析的数据，可以是字符串、对象或数组。

字符串示例：

```json
"state": "客户反馈同一订单被扣款两次，并且三天没有收到回复。"
```

对象示例：

```json
"state": {
  "customer_level": "VIP",
  "subject": "重复扣款",
  "message": "同一订单被扣款两次，三天没有回复",
  "waiting_days": 3
}
```

数组示例：

```json
"state": [
  {
    "role": "customer",
    "message": "同一订单被扣款两次"
  },
  {
    "role": "support",
    "message": "工单仍在处理中"
  }
]
```

JEV 按输入 Token 计费，因此应只传入完成判断所需的信息。

### `questions`

`questions` 定义需要 JEV 作出的判断。它必须是非空 JSON 对象：

```json
"questions": {
  "escalate": {
    "type": "noul",
    "instructions": "是否应该立即转交人工处理？"
  }
}
```

`escalate` 是自定义问题名称。响应中的答案会使用相同名称：

```json
"answers": {
  "escalate": {
    "type": "noul",
    "noul": 0.94
  }
}
```

## 四、问题类型

JEV 支持 `noul`、`choice` 和 `score` 三种问题类型。同一次请求可以同时提出多个不同类型的问题。

### 1. `noul`：是或否的概率

请求：

```json
"escalate": {
  "type": "noul",
  "instructions": "是否应该立即转交人工处理？"
}
```

响应：

```json
"escalate": {
  "type": "noul",
  "noul": 0.94
}
```

`noul` 的范围是 `0–1`：

- 越接近 `1`，越倾向“是”；
- 越接近 `0`，越倾向“否”。

业务系统应自行设置阈值，例如：

```text
noul >= 0.85  → 自动转人工
0.50–0.85     → 进入复核队列
noul < 0.50   → 不转人工
```

### 2. `choice`：从多个选项中选择一个

请求：

```json
"route": {
  "type": "choice",
  "instructions": "应该把工单交给哪个部门？",
  "criteria": {
    "billing": "扣款、退款和发票问题",
    "technical": "产品故障或功能异常",
    "account": "登录、账号和权限问题"
  }
}
```

响应示例：

```json
"route": {
  "type": "choice",
  "choice": "billing",
  "confidence": 0.99,
  "probabilities": {
    "billing": 0.99,
    "technical": 0.01,
    "account": 0.0
  }
}
```

- `choice`：最终选中的选项；
- `confidence`：此次判断的置信度；
- `probabilities`：每个选项对应的概率。

`criteria` 的键应使用稳定的机器标识，值用于清楚描述各选项的含义。

### 3. `score`：按有序等级评分

请求：

```json
"urgency": {
  "type": "score",
  "instructions": "这张工单有多紧急？",
  "criteria": [
    "普通，无需立即处理",
    "需要在今天处理",
    "紧急",
    "非常紧急，必须立即处理"
  ]
}
```

响应示例：

```json
"urgency": {
  "type": "score",
  "score": 2.97,
  "confidence": 1.0,
  "probabilities": {
    "0": 0.0,
    "3": 1.0
  }
}
```

`criteria` 必须按从低到高的顺序填写，建议提供清晰、具体且互相可区分的等级描述。

## 五、完整请求示例

```json
{
  "model": "jev-latest",
  "stream": false,
  "input": "分析客服工单并给出结构化判断",
  "state": {
    "customer_level": "VIP",
    "subject": "重复扣款",
    "message": "同一订单被扣款两次，三天没有收到回复",
    "waiting_days": 3
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
    "urgency": {
      "type": "score",
      "instructions": "这张工单有多紧急？",
      "criteria": [
        "普通，无需立即处理",
        "需要在今天处理",
        "紧急",
        "非常紧急，必须立即处理"
      ]
    },
    "escalate": {
      "type": "noul",
      "instructions": "是否应该立即转交人工处理？"
    }
  }
}
```

## 六、cURL 示例

```bash
curl https://your-sub2api.example.com/v1/responses \
  -H "Authorization: Bearer sk-你的Sub2API用户Key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "jev-latest",
    "stream": false,
    "input": "分析客服工单",
    "state": "客户反馈同一订单被扣款两次，并且三天没有收到回复。",
    "questions": {
      "escalate": {
        "type": "noul",
        "instructions": "是否应该立即转交人工处理？"
      }
    }
  }'
```

## 七、Python 示例

### 使用 requests

```python
import json
import requests

response = requests.post(
    "https://your-sub2api.example.com/v1/responses",
    headers={
        "Authorization": "Bearer sk-你的Sub2API用户Key",
        "Content-Type": "application/json",
    },
    json={
        "model": "jev-latest",
        "stream": False,
        "input": "分析客服工单",
        "state": {
            "subject": "重复扣款",
            "message": "同一订单被扣款两次，三天没有回复",
        },
        "questions": {
            "escalate": {
                "type": "noul",
                "instructions": "是否应该立即转交人工处理？",
            }
        },
    },
    timeout=180,
)

response.raise_for_status()
data = response.json()

# 推荐直接读取顶层 JEV 结构
print(data["jev"]["answers"])

# 也可以解析标准 Responses 文本字段
text_result = json.loads(data["output"][0]["content"][0]["text"])
print(text_result["answers"])
```

### 使用 OpenAI Python SDK

```python
import json
from openai import OpenAI

client = OpenAI(
    api_key="sk-你的Sub2API用户Key",
    base_url="https://your-sub2api.example.com/v1",
)

response = client.responses.create(
    model="jev-latest",
    input="分析客服工单",
    extra_body={
        "state": "客户反馈被重复扣款，并且三天没有收到回复。",
        "questions": {
            "escalate": {
                "type": "noul",
                "instructions": "是否应该立即转交人工处理？",
            }
        },
    },
)

result = json.loads(response.output_text)
print(result["answers"]["escalate"])
```

## 八、JavaScript 示例

```javascript
const response = await fetch("https://your-sub2api.example.com/v1/responses", {
  method: "POST",
  headers: {
    Authorization: "Bearer sk-你的Sub2API用户Key",
    "Content-Type": "application/json"
  },
  body: JSON.stringify({
    model: "jev-latest",
    stream: false,
    input: "分析客服工单",
    state: {
      subject: "重复扣款",
      message: "同一订单被扣款两次，三天没有回复"
    },
    questions: {
      escalate: {
        type: "noul",
        instructions: "是否应该立即转交人工处理？"
      }
    }
  })
});

if (!response.ok) {
  throw new Error(`HTTP ${response.status}: ${await response.text()}`);
}

const data = await response.json();
console.log(data.jev.answers);
```

## 九、响应格式

成功响应遵循 OpenAI Responses 格式，并额外提供顶层 `jev` 字段：

```json
{
  "id": "resp_jev_...",
  "object": "response",
  "status": "completed",
  "model": "jev-1.13.0",
  "output": [
    {
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "output_text",
          "text": "{\"model\":\"jev-1.13.0\",\"answers\":{...},\"usage\":{...}}"
        }
      ]
    }
  ],
  "usage": {
    "input_tokens": 62,
    "output_tokens": 0,
    "total_tokens": 62
  },
  "jev": {
    "model": "jev-1.13.0",
    "answers": {
      "escalate": {
        "type": "noul",
        "noul": 0.94
      }
    },
    "usage": {
      "input_tokens": 62,
      "cost_usd": 0.000026
    }
  }
}
```

推荐读取：

```text
$.jev.answers
```

若客户端只支持标准 OpenAI Responses 字段，则读取并解析：

```text
$.output[0].content[0].text
```

插件不会向下游暴露 JEV 账户的剩余余额。

## 十、失败判断

不要只根据 HTTP 200 判断业务成功，还应检查：

```text
status == "completed"
```

JEV 上游鉴权、余额、限流或网络异常可能返回：

```json
{
  "status": "failed",
  "error": {
    "type": "jev_error",
    "code": "jev_insufficient_credits",
    "message": "JEV 上游余额不足"
  }
}
```

常见错误码：

| 错误码 | 含义 |
|---|---|
| `jev_invalid_request` | 缺少 `state`、`questions` 或字段格式错误 |
| `jev_credentials_invalid` | 插件中配置的 JEV Key 无效或账号不可用 |
| `jev_insufficient_credits` | JEV 上游余额不足 |
| `jev_rate_limited` | JEV 上游触发速率限制 |
| `jev_transport_error` | 服务器无法连接 JEV |
| `jev_invalid_response` | JEV 返回了无法解析的响应 |

## 十一、定价说明

JEV 官方按输入 Token 计费，输出免费。当前建议的 Sub2API 定价为：

```text
输入价格：0.42 USD / 百万 Token
输出价格：0
缓存价格：0
```

如果分组倍率设置为 `1.5×`，用户最终价格为：

```text
0.42 × 1.5 = 0.63 USD / 百万输入 Token
```

后台基础价格填写 `0.42`，不要填写 `0.63`，否则分组倍率会再乘一次。

## 十二、聊天模式说明

JEV 不是聊天模型，当前插件不支持通过 `/v1/chat/completions` 进行普通对话。

以下用法不受支持：

```json
{
  "model": "jev-latest",
  "messages": [
    {
      "role": "user",
      "content": "你好，请介绍一下自己"
    }
  ]
}
```

请使用：

```text
POST /v1/responses
```

并提供 `state` 和 `questions`。

若测试工具提示“消息中不包含有效的 JSON，无法提取消息内容”，通常表示工具正在按 Chat Completions 格式读取 `choices[0].message.content`。请切换到普通 HTTP/Raw JSON 模式，并查看完整响应或配置 JSONPath：

```text
$.jev.answers
```

## 十三、部署要求

为了让 Sub2API 当前的插件系统调度 JEV：

1. 插件必须已启用且运行健康；
2. 插件灰度建议设为 `100%`；
3. JEV 专用分组需要绑定至少一个可调度的 OpenAI OAuth 账号；
4. 分组模型白名单需要允许 `jev-latest` 或相应固定版本；
5. 渠道定价中需要存在对应 JEV 模型；
6. 调用使用绑定到 JEV 分组的 Sub2API 用户 Key。

OpenAI OAuth 账号在这里是 Sub2API 插件系统的调度入口。JEV 请求命中插件后不会发送给 OpenAI，插件会使用服务器中保存的 JEV Key 请求 JEV 上游。

## 十四、安全建议

- 不要将 JEV Key写入客户端代码或请求 Body；
- 不要向终端用户提供管理员 Key；
- 对话、日志或截图中暴露过的 JEV Key应立即轮换；
- 生产环境建议固定 JEV 模型版本并对判断阈值做业务验证；
- 对高风险自动化操作，应保留人工复核或安全兜底机制。
