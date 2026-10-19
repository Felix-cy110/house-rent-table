# 三家模型适配与调研记录

核对日期：2026-10-19。目标是只让用户选择模型并输入自己的 API Key，固定官方端点和模型 ID，始终开启思考；业务侧不解释厂商响应字段。

## 固定配置

唯一配置表为 `internal/modelconfig/providers.go`，前端从 `GET /api/ai/models` 获取展示信息。`POST /api/ai/connect` 只接受 `{provider, apiKey}`；未知字段（包括 URL、model、thinking）及未知 provider 均拒绝。

| provider | 模型 ID | 固定推理端点 | 显式思考设置 |
| --- | --- | --- | --- |
| mimo | `mimo-v2.6-flash` | `https://api.xiaomimimo.com/v1/chat/completions` | `thinking.type=enabled` |
| glm | `glm-5.3-flash` | `https://open.bigmodel.cn/api/paas/v4/chat/completions` | `thinking.type=enabled`、`thinking.clear_thinking=false`、`reasoning_effort=max` |
| deepseek | `deepseek-flash` | `https://api.deepseek.com/chat/completions` | `thinking.type=enabled`、`reasoning_effort=high` |

三家均使用 Bearer 认证和非流式 Chat Completions。MiMo 同时支持 `api-key` 认证，本实现采用其明确支持的 Bearer 形式。避免为思考模型发送无效的 temperature/top_p 等采样覆盖。连接验证、主分析、工具续传和独立后台提取都使用上述思考配置。

DeepSeek 官方模型表说明：旧 `deepseek-v4-flash` 名称仍接受，但对应旧模型已退役，请求由 **DeepSeek V4.1-Flash-1017** 服务。按用户选择使用官方推荐别名 `deepseek-flash`；页面标明截至日期和实际版本，注明别名可能更新。这不是锁定快照版本的承诺。提取元数据优先记录响应中的 model，未提供时回退到请求 ID。

## 为什么选择 Chat Completions

统一结果对象不要求三家使用 OpenAI Responses 端点。MiMo 的联网搜索专页明确限定当前使用 Chat Completions；三个目标模型均支持 Chat Completions、思考和函数调用。因此当前只保留这一条推理协议，减少额外转换及故障重试，不保留任意 Responses URL 输入。

智谱另有独立的 Responses 基址 `https://open.bigmodel.cn/api/v1`，与 Chat 的 `/api/paas/v4` 不同，不能简单替换路径。其 Responses 文档列出的结构化格式是 text/json_object，也不能将 OpenAI 严格 json_schema 参数视为通用能力。

## 搜索适配

| 模型 | 实现 | 请求与响应差异 |
| --- | --- | --- |
| MiMo | 内置 `tools: [{type: web_search, max_keyword: 3}, ...]` | 用户需先开通 Web Search 插件；响应来源主要在 `choices[].message.annotations`，可能没有字符位置；`message.error_message` 表示搜索异常；可选 `usage.web_search_usage` 提供搜索次数和页数 |
| GLM | `rental_web_search` 函数 → `https://open.bigmodel.cn/api/paas/v4/web_search` | 同一 Key；请求为 `search_query`、`search_engine=search_pro`、`search_intent=false`、`count=5`、`content_size=high`；查询限制 70 字符；结果来自 `search_result`，使用 link/content/media/publish_date |
| DeepSeek | `rental_web_search` 函数 → `https://api.deepseek.com/v1/tools/search` | 同一 Key；请求为 query/count=5/need_content=false；查询限制 128 个 Unicode 字符；结果来自 results，使用 url/summary/snippet/site_name/date |

GLM 通用 Chat 文档描述了内置 web_search，但 **GLM 5.3-Flash 的专属视觉模型请求结构 tools 仅列 FunctionToolSchema**。因此不假定该目标模型支持通用内置搜索；独立 Web Search API 的能力和字段有明确文档依据。归一化层仍兼容 GLM 响应顶层可选 web_search 来源，业务侧无需变化。

独立搜索每次分析最多 8 次、每次最多取 5 项，单次超时 30 秒；参数无效和预算用尽不发送请求。MiMo 的 `max_keyword=3` 是每轮关键词限制，不是整个报告的搜索费用上限。所有模型最多 16 轮工具交互、每轮最多 64 个函数调用。未知工具只能收到错误，不执行 shell、文件访问或任意 HTTP。

搜索资料作为外部数据，明确要求忽略其中的指令；仅查询必要的区域、小区和费用项目，不把联系人或整份表格作为查询词。程序不自行访问返回页面 URL。结果链接仅接受不含账号密码的 HTTP(S)。模型仍负责语义筛选；搜索命中本身不代表房源可比或价格已经核实。

独立搜索失败会回传固定、安全的工具错误并附报告提示，允许继续费用核算。没有来源时报告显示核实提醒。MiMo 整个模型请求因未开插件等原因失败时直接报错，不自动关闭搜索或切换模型。搜索可能单独计费；连接探测不测试搜索权限。

## 轻量归一化边界

```text
固定 provider + Key
  → chatSession：厂商请求编码、私有思考续传
  → normalize：NormalizedResponse
  → 共用工具循环
  → Analyze / Extract → HTTP / 页面 / 持久化
```

`NormalizedResponse` 包含 Provider、Model、Text、Finish、ToolCalls、Sources、Citations、Warnings、Usage。正文只从 content 读取，不将 reasoning_content 拼进报告或提取 JSON。业务结果保持原有 text 字段，增加可选的来源和提示；业务侧没有厂商分支。

| 归一化内容 | 处理规则 |
| --- | --- |
| 正文和结束状态 | stop → completed；tool_calls → 等待工具；length、上下文超限及重复截断 → incomplete；content_filter/sensitive → refused；其他未知结束原因 → failed。仅完整非空正文可作为最终结果 |
| 工具调用 | 统一为 ID、Name、Arguments；检查 ID、重复 ID、函数名、工具类型、数量和消息结构；本地执行只允许计算与已配置搜索 |
| 私有续传 | 完整保留 reasoning_content 的字符串、缺失/null 状态以及函数调用；不把未识别的 response-only 扩展回传，不将思考内容暴露给业务、浏览器或日志 |
| 来源 | MiMo annotations、GLM 顶层 web_search 和独立搜索结果统一为 Title、URL、Site、PublishedAt；去重、限制总数 100；无位置时仅列来源，不造引用坐标 |
| 引用 | 只接受有效 URL 和有效字符范围，统一为 Unicode 码点索引；最终正文中的引用与历轮来源分开保存 |
| 用量 | prompt/completion/total/reasoning/cached tokens 统一；DeepSeek 兼容顶层 prompt_cache_hit_tokens、prompt_cache_miss_tokens；MiMo 读取可选搜索计数；未知为 nil，明确的 0 保留为 0，不估算缺失数据 |
| 多轮用量 | 只在相关轮次都提供计数时累加；缺任一轮则对应总量未知，防止把不完整统计当成全量；这不是费用计算器 |
| 可选扩展 | model、usage、详情对象、来源和日期缺失/null 均可处理；无法识别的可选元数据忽略；未知扩展忽略。不可恢复的正文/思考/工具结构错误拒绝，不掩盖协议失败 |
| 错误 | HTTP 错误和响应 error 统一失败，返回本地固定消息，不把供应商原始错误、Key 或请求内容回显 |

配置及密钥仅保存在本机配置文件，HTTP 重定向被拒绝。旧 `{apiKey,baseUrl,model}` 文件标记 needsReconnect；不按 URL 猜供应商，不复用旧 Key。用户显式重新连接成功后原子替换，失败保留旧文件；清除操作删除文件。

## 结构化提取与原业务

三家文档均描述 JSON 模式；本实现使用 `response_format: {type: json_object}`，并将完整 `extraction.Schema()` 加入专属系统指令。不会假设其支持 OpenAI `json_schema`/strict。JSON 模式只约束语法，原有 `internal/extraction` Schema、证据和业务组合校验仍在入库前执行。

Excel 导入、字段原文和加粗目标、精确计算器、报告取消、独立后台队列及中介反馈流程继续使用原接口。提取仍不带搜索和计算工具，不加载主报告规则；与主报告分开快照配置、分开取消。provider 元数据从通用网关主机改为固定标识，历史数据不回填。此次没有数据库迁移。

开启思考可能增加耗时和用量。连接探测超时由 30 秒调整为 90 秒；HTTP 接口外层为 120 秒，主分析仍受原有 5 分钟限制。搜索存在账号权限和费用差异，需用户在供应商侧开通。

## 官方资料

以下为本次实际读取的官方文档；无需用户 Key 即可查阅。网页内容可能随时间变更，以对应日期核对结果为准。

- MiMo [Chat API](https://mimo.mi.com/static/docs/api/chat/openai-api.md)、[思考模式](https://mimo.mi.com/static/docs/quick-start/usage-guide/text-generation/deep-thinking.md)、[联网搜索](https://mimo.mi.com/static/docs/quick-start/usage-guide/text-generation/tool-calling/web-search.md)、[JSON 输出](https://mimo.mi.com/static/docs/quick-start/usage-guide/text-generation/structured-output.md)。
- 智谱 [GLM 5.3-Flash](https://docs.bigmodel.cn/cn/guide/models/vlm/glm-5.3-flash.md)、[Chat API](https://docs.bigmodel.cn/api-reference/模型-api/对话补全.md)、[思考模式](https://docs.bigmodel.cn/cn/guide/capabilities/thinking-mode.md)、[独立搜索 API](https://docs.bigmodel.cn/api-reference/工具-api/网络搜索.md)、[JSON 输出](https://docs.bigmodel.cn/cn/guide/capabilities/struct-output.md)、[Responses 差异](https://docs.bigmodel.cn/cn/guide/develop/responses/introduction.md)。
- DeepSeek [模型与别名](https://api-docs.deepseek.com/quick_start/pricing)、[2026-10-17 发布说明](https://api-docs.deepseek.com/news/news261017)、[Chat API](https://api-docs.deepseek.com/api/create-chat-completion)、[思考模式](https://api-docs.deepseek.com/guides/thinking_mode)、[Search API](https://api-docs.deepseek.com/guides/search)、[JSON 输出](https://api-docs.deepseek.com/guides/json_mode)。

## 验证边界

Go 测试使用本地 HTTP 或注入传输，覆盖三家固定地址/模型/认证/思考设置、两种独立搜索请求、思考工具续传、来源差异、可选字段、usage 空值与零、错误终止、重连迁移和取消。浏览器通过 `e2e` 专属测试服务覆盖真实业务处理器、配置保存、上传、思考续传与计算器；生产程序不开放 URL 覆盖参数。

2026-10-19：全量 Go 测试、go vet、构建、模板检查与 30 项 Playwright 测试通过。未使用真实供应商 Key，不把模拟验证写成真实服务联调；模型质量、账号搜索权限和实际用量仍需真实环境验证。未重新运行真实 MySQL 集成测试。
