# JEV 模型用户使用指南

JEV 是一个专门用于分类、评分和判断的结构化决策模型。

与普通聊天模型不同，JEV 不生成长篇回答，而是直接返回稳定、可供程序读取的判断结果。它适合客服分流、内容审核、风险判断、线索评分、RAG 相关性判断等场景。

## JEV 适合做什么

JEV 特别适合回答以下问题：

- 这条工单应该分配给哪个部门？
- 这段内容是否存在风险？
- 这个客户是否需要立即转人工？
- 这条销售线索的质量有多高？
- 这段资料与用户问题是否相关？
- 这次工具调用应该允许、确认还是阻止？

JEV 不适合：

- 普通聊天；
- 写文章、翻译或总结长文；
- 生成代码；
- 开放式知识问答；
- 生成图片、音频或视频。

简单来说：

```text
普通大模型负责“生成内容”
JEV 负责“作出判断”
```

## 如何使用

调用 JEV 时，需要提供三个主要字段：

```text
input     = 本次任务的简短名称
state     = 需要分析的实际资料
questions = 希望 JEV 作出的判断
```

最简单的请求示例：

```json
{
  "model": "jev-latest",
  "stream": false,
  "input": "判断是否需要转人工",
  "state": "客户反馈同一订单被扣款两次，并且三天没有收到回复。",
  "questions": {
    "escalate": {
      "type": "noul",
      "instructions": "是否应该立即转交人工处理？"
    }
  }
}
```

## 字段说明

### `input`：任务名称

`input` 用一句简短的话说明本次任务：

```json
"input": "分析客服工单"
```

推荐写法：

- `分析客服工单`
- `判断内容风险`
- `评估销售线索`
- `检查资料相关性`

不要把大量待分析内容都放在 `input` 中，实际资料应放在 `state`。

### `state`：需要分析的资料

`state` 是 JEV 作出判断时参考的实际内容。

它可以是一段文字：

```json
"state": "客户反馈被重复扣款，并且三天没有收到回复。"
```

也可以是结构化对象：

```json
"state": {
  "customer_level": "VIP",
  "subject": "重复扣款",
  "message": "同一订单被扣款两次，三天没有回复",
  "waiting_days": 3
}
```

建议只提供作出判断所需的信息。内容越精简，响应通常越快，费用也越低。

### `questions`：希望得到的判断

`questions` 用来告诉 JEV，你希望它针对 `state` 判断什么。

```json
"questions": {
  "escalate": {
    "type": "noul",
    "instructions": "是否应该立即转交人工处理？"
  }
}
```

其中：

- `escalate` 是自定义的答案名称；
- `type` 是问题类型；
- `instructions` 是具体的判断要求。

一次请求可以同时提出多个问题。

## 三种问题类型

### 1. 是非判断：`noul`

适合判断“是否”“能否”“有没有风险”等问题。

```json
"escalate": {
  "type": "noul",
  "instructions": "是否应该立即转交人工处理？"
}
```

返回示例：

```json
"escalate": {
  "type": "noul",
  "noul": 0.94
}
```

`noul` 是 `0–1` 之间的概率：

- 越接近 `1`，越倾向“是”；
- 越接近 `0`，越倾向“否”。

例如，可以设置以下业务规则：

```text
0.85–1.00：自动转人工
0.50–0.85：进入人工复核
0.00–0.50：无需转人工
```

具体阈值应根据实际业务数据调整。

### 2. 分类选择：`choice`

适合从多个候选项中选择一个结果。

```json
"route": {
  "type": "choice",
  "instructions": "应该把这张工单交给哪个部门？",
  "criteria": {
    "billing": "扣款、退款和发票问题",
    "technical": "产品故障或功能异常",
    "account": "登录、账号和权限问题"
  }
}
```

返回示例：

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

其中：

- `choice` 是最终选择；
- `confidence` 是置信度；
- `probabilities` 展示各选项的概率。

### 3. 等级评分：`score`

适合判断紧急程度、质量、风险等级等有顺序的结果。

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

返回示例：

```json
"urgency": {
  "type": "score",
  "score": 2.97,
  "confidence": 1.0
}
```

`criteria` 必须按照从低到高的顺序填写。

## 常用场景示例

### 客服工单分流

```json
{
  "model": "jev-latest",
  "stream": false,
  "input": "客服工单分流",
  "state": {
    "subject": "重复扣款",
    "message": "我被扣款两次，三天了仍然没人回复。",
    "customer_level": "VIP"
  },
  "questions": {
    "route": {
      "type": "choice",
      "instructions": "应该把工单交给哪个部门？",
      "criteria": {
        "billing": "扣款、退款、账单和发票",
        "technical": "产品故障和功能异常",
        "account": "登录、账号和权限"
      }
    },
    "urgency": {
      "type": "score",
      "instructions": "工单的紧急程度如何？",
      "criteria": ["普通", "今天处理", "紧急", "立即处理"]
    },
    "escalate": {
      "type": "noul",
      "instructions": "是否应该立即转交人工？"
    }
  }
}
```

### 内容风险判断

```json
{
  "model": "jev-latest",
  "stream": false,
  "input": "判断内容风险",
  "state": "待审核的用户内容放在这里。",
  "questions": {
    "unsafe": {
      "type": "noul",
      "instructions": "这段内容是否包含需要拦截的高风险信息？"
    },
    "risk_level": {
      "type": "score",
      "instructions": "评估这段内容的风险等级。",
      "criteria": ["无明显风险", "轻微风险", "较高风险", "严重风险"]
    }
  }
}
```

### RAG 资料相关性

```json
{
  "model": "jev-latest",
  "stream": false,
  "input": "判断检索资料相关性",
  "state": {
    "question": "如何更换账户的 API Key？",
    "document": "进入设置中的 API Key 页面，撤销旧 Key，然后创建新 Key。"
  },
  "questions": {
    "relevant": {
      "type": "noul",
      "instructions": "这份资料是否能够直接帮助回答用户问题？"
    },
    "relevance": {
      "type": "score",
      "instructions": "资料与问题的相关程度如何？",
      "criteria": ["无关", "部分相关", "基本相关", "直接回答"]
    }
  }
}
```

### 工具调用风险检查

```json
{
  "model": "jev-latest",
  "stream": false,
  "input": "检查工具调用风险",
  "state": {
    "goal": "清理构建目录并部署网站",
    "tool": "shell",
    "arguments": "删除构建目录并同步到生产存储，同时删除目标端多余文件"
  },
  "questions": {
    "action": {
      "type": "choice",
      "instructions": "应该如何处理这个工具调用？",
      "criteria": {
        "allow": "风险低，可以自动执行",
        "confirm": "存在风险，执行前需要用户确认",
        "block": "风险过高，应该阻止执行"
      }
    },
    "risk": {
      "type": "score",
      "instructions": "评估这个操作的风险等级。",
      "criteria": ["低风险", "中等风险", "高风险", "严重且不可逆"]
    }
  }
}
```

## 如何查看结果

推荐读取响应中的：

```text
jev.answers
```

示例：

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

如果使用只支持标准 OpenAI Responses 格式的工具，也可以读取：

```text
output[0].content[0].text
```

该字段是包含 JEV 结果的 JSON 字符串。

## 提高判断质量的技巧

### 提供必要事实

不推荐：

```json
"state": "这个客户有问题"
```

推荐：

```json
"state": {
  "message": "客户反馈同一订单被扣款两次",
  "waiting_days": 3,
  "customer_level": "VIP"
}
```

### 清楚描述判断标准

不推荐：

```json
"instructions": "判断一下"
```

推荐：

```json
"instructions": "根据问题是否涉及扣款、退款或发票，判断是否应该分配给账单部门。"
```

### 让选项互相区分

`choice` 中的选项应当含义明确，尽量避免多个选项描述同一件事。

### 同一份资料尽量合并提问

多个问题可以共享同一个 `state`。一次请求同时完成分类、评分和风险判断，通常比多次重复发送相同资料更高效。

### 根据实际数据调整阈值

`noul` 和 `confidence` 是概率，不代表固定的业务规则。上线自动处理前，应使用真实业务样本测试并确定合适阈值。

## 计费说明

JEV 按输入 Token 计费，输出免费。

当前用户价格：

```text
$0.63 / 百万输入 Token
输出 Token 免费
```

最终价格以模型广场或账户所属分组展示为准。

降低费用的方法：

- 删除与判断无关的上下文；
- 多个问题共享同一个 `state`；
- 避免重复发送相同资料；
- 优先使用结构清晰的对象，而不是冗长描述。

## 重要限制

### 不能作为普通聊天模型使用

JEV 不支持普通的 `messages` 对话格式，也不会像聊天助手一样生成自然语言回答。

请使用 Responses 请求格式，并提供：

```text
state + questions
```

### 建议关闭流式输出

推荐设置：

```json
"stream": false
```

JEV 会在判断完成后返回完整结果，开启流式输出不会让上游逐字生成内容。

### 自动决策需要保留安全兜底

涉及资金、封号、医疗、法律或其他高风险操作时，不应仅凭一次模型结果直接执行不可逆操作。建议结合置信度、业务规则和人工复核。

## 常见问题

### 为什么不能直接聊天？

因为 JEV 的目标是输出可靠的结构化判断，而不是生成自由文本。

### 为什么返回的是 JSON？

结构化 JSON 能让业务程序直接读取结果，无需从自然语言中猜测答案，也更适合自动化流程。

### `input` 和 `state` 有什么区别？

- `input` 是任务名称；
- `state` 是实际需要分析的资料。

### 一次可以问多个问题吗？

可以。多个问题会共享同一份 `state`，并在一次请求中返回全部答案。

### 应该使用 `jev-latest` 还是固定版本？

- 测试和快速接入：使用 `jev-latest`；
- 对判断阈值稳定性要求较高：使用经过验证的固定版本。

### 为什么测试工具提示无法提取消息？

部分工具只识别普通聊天响应。请使用普通 HTTP/Raw JSON 模式，并查看完整响应中的：

```text
jev.answers
```
