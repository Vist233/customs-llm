# 设计文档

项目名:`customs-llm`。仓库为 `Vist233/customs-llm`。命令行命令暂用 `customs`(见第 10 节)。

## 1. 要解决的问题

LLM 请求出问题时,你不知道是哪一环造成的。

链路是 `客户端 → (中转/网关) → 上游 → 模型`。出问题时只有症状:请求失败、卡住、模型答非所问、缓存没命中。
客户端报的错常常被改写过(opencode 的 `Provider request failed with HTTP 400`),网关日志只是它自己的说法,
没有任何一环的真实输入输出摆在你面前。

本程序是一个**查看程序**:把一次请求在每一跳的输入、输出、耗时和错误按层级展开,并标出每条信息的证据来源。

数据依据:对 litellm、new-api、one-api、claude-code-router、Portkey、magpie、opencode 的 issue 的调研,以及
`testset/` 里 33 条用例。结论见对话记录,要点是这类问题有两大类:请求方向的语义丢失和响应方向的流式、错误、用量被改坏,
另有一类在 opencode Go 上占多数:卡住和错误被吞。

## 2. 原则

1. **证据带出处。** 每条记录标明来源层级,界面上随时可见:
   - `wire`:我们的中转亲眼所见,可信度最高。
   - `reported`:网关自己的日志,是嫌疑人自己的说法。
   - `inferred`:从响应推断出来的。
2. **程序里没有 AI。** 所有判断是确定性规则。AI 只可能出现在用户自己的 Agent 里(第 9 节,可选)。
3. **不改已有配置,只做加法式的临时覆盖。** 默认模式只读日志并起一个本地中转。需要看网关出站字节时,新增一个带标记的测试条目(第 9 节),测完删除;已有的模型、渠道、provider 一概不动。
4. **展示按用户关心的问题组织,不直接展示 JSON。** JSON 只是最后一个标签页。
5. **结论陈述事实,不下裁决。** 例如"出站少了 1 张图片",而不是"网关有 bug"。

## 3. 非目标(现阶段)

- **脱敏:先不做。** 记录原样保存。因此:数据库文件权限 0600,所有端口只监听 `127.0.0.1`,界面顶部常驻提示"本地记录含密钥与对话原文"。以后要分享报告再做脱敏。
- 请求重放与多目标对比、多种协议的测试模型:都在第 13 节的后期里程碑。
- 托管版、多用户、云端存储。

## 4. 架构

一个 Go 进程,若干互相独立的模块,通过统一的记录模型(第 5 节的 Hop)连接。

```
                 ┌──────── 来源(产生 Hop) ─────────┐
                 │ relay      前置中转,wire 层      │
                 │ fakeup     假上游,wire 层        │
                 │ collectors 读日志/API,reported 层│
                 └───────────────┬──────────────────┘
                                 ▼
   drivers ──(新增/删除测试条目)  store(SQLite + 内容寻址 blob)
   (每个网关一个)                 │
                                 ▼
                 ┌──────── 解析与判定 ──────────────┐
                 │ ir       各协议 → 语义项          │
                 │ checks   数据集断言求值器          │
                 └───────────────┬──────────────────┘
                                 ▼
                      api(JSON + SSE) ──► web(内嵌前端)
```

### 4.1 模块

| 模块 | 职责 | 备注 |
|---|---|---|
| `relay` | 前置中转:按路径前缀转发到配置好的目标,逐块转发并立即刷新,同时记录 | 程序启动时一起启动,见第 7 节 |
| `fakeup` | 假上游:接收网关发来的测试请求,记录字节,返回预设响应 | 第一版只实现 OpenAI 兼容协议 |
| `collect/<name>` | 采集器:读某个软件的日志或调它的 API,产生 `reported` 层 Hop | 每个软件一个,见第 6 节 |
| `driver/<name>` | 驱动:对某个网关新增、删除测试条目 | 见第 9 节;与采集器分开,同一网关可以只有其中之一 |
| `store` | SQLite(纯 Go 驱动,免 CGO)加内容寻址的 blob | 请求体按哈希去重,对话历史里大量重复 |
| `ir` | 把各协议的请求与响应解析成统一的语义项 | 见 4.2 |
| `testset`(目录)与 `internal/testset`(加载与校验) | 独立的测试集:`testset/cases/<网关>/<id>.json`,`cmd/tsctl` 负责校验、列表、统计 | 与产品代码解耦,新增用例只加 JSON |
| `checks` | 测试集断言求值器 | 复用 `internal/testset`,按断言的 `target` 取消息,不关心消息来源 |
| `api` | 给前端的 JSON 接口和 SSE 推送 | 列表接口不返回消息体 |
| `web` | 内嵌的前端 | 见第 8 节 |

目录:`internal/{model,store,relay,fakeup,collect/<name>,driver/<name>,ir,checks,testset,bugsim,api}`,命令在 `cmd/{customs,tsctl}`,测试集在 `testset/`,前端在 `web/`。

### 4.2 `ir` 是枢纽

对账表和检查项都建立在 `ir` 上。它把请求与响应解析成统一的语义项:

- 消息(角色、内容块序列)、内容块(文本、图片、文档、思考、工具调用、工具结果)
- 工具定义、`tool_choice`、采样参数、推理控制、缓存标记、元数据
- 用量、结束原因、流事件序列

没有它,跨协议比较就退化成逐字段 JSON Diff,全是噪音。代价是:每个协议的解析器写错一处就会产生误报,
所以解析器必须有自己的测试,用 `testset/` 的用例和各协议的官方样例做夹具。

第一版只解析 **OpenAI Chat** 和 **Anthropic Messages**。其他协议在详情页回退为原始 JSON,并明确标注"该协议暂未解析",
不猜测、不显示半截的对账表。

### 4.3 约束

1. **转发路径不能被存储拖慢。** 写库走独立队列;队列满了丢事件并计数,绝不阻塞转发。
2. **流式不缓冲。** 中转和假上游逐块转发,每个事件带到达时间。
3. **清理要可靠。** 驱动新增测试条目前先写清单文件;启动时先清理上次遗留;退出时用信号处理清理。
4. **只依赖一个契约。** 来源只产生 Hop,解析和展示只读 Hop。新增一个软件的支持不应改动其他模块。
5. **只监听回环地址。** 中转、假上游、前端接口都一样。

存储用 SQLite;请求体、响应体、流式事件按内容哈希去重存放。

### 4.4 可测试性

目标:没有真实网关、没有网络,也能测完绝大部分逻辑。

| 层次 | 测什么 | 怎么测 |
|---|---|---|
| 单元 | `ir` 各协议解析器;测试集加载与校验;游标和去重 | 官方样例与测试集用例做夹具,golden 文件比对 |
| 组件 | `relay`(流式逐块转发、首字节时间、上游失败时记录原始错误体);`fakeup`;各采集器 | `httptest` 起上游;采集器读夹具日志,并模拟追加写入、轮转和原地裁剪 |
| 集成 | 整条管线:来源 → 存储 → `ir` → `checks` → `api` | `bugsim`:一个**有缺陷网关的模拟器**,按测试集的 `examples.bad` 重现每个已知缺陷,`examples.good` 是修好后的行为 |
| 端到端 | 真实网关 | 手动或可选的 CI 任务;跑过的用例才把 `verified` 改为 `true` |

关键设计:

1. **测试集自检。** 每个用例的断言必须在 `examples.bad` 上失败、在 `examples.good` 上通过。这样求值器的正确性不依赖真实网关,
   也防止写出"永远通过"的断言。没有 `examples` 的用例在统计里单独标出,优先补齐。
2. **确定性。** 时钟、ID 生成器、随机源都通过接口注入;存储支持内存或临时目录;blob 存储是接口。
3. **录制与回放。** `customs export --trace <id>` 导出一个自包含的包(Hop、事件、blob)。真实遇到的问题可以直接变成夹具:
   导出、提交进仓库、在测试里喂回管线,不需要网络。
4. **原始数据是真相,派生数据可重建。** `ir`、标记、摘要都能由原始 Hop 重新算出(`customs reindex`),所以解析器升级不需要丢数据,
   测试也只需要固定原始输入。
5. **CI:** `go vet`、`go test ./...`、`tsctl validate`。

## 5. 数据模型

```
Trace            一次用户请求,可能跨多跳
  id, started_at, client(识别出的客户端), model, 关联键们
  └─ Hop[]       一跳
       source        wire | reported | inferred
       collector     relay | litellm | opencode | magpie | new-api | ...
       from, to      例如 client → litellm
       request       方法、路径、查询串、头、体
       response      状态码、头、体
       events[]      流式事件(每个 data: 一条),带到达时间
       timing        开始、首字节、结束;各事件间隔
       usage         input、output、cache_read、cache_write、reasoning
       finish        finish_reason / stop_reason,以及是否收到终止事件
       error         原始错误体(先于任何包装)
       raw_ref       指向日志里的原始行或表行
```

### 把不同来源的记录归到同一个 Trace

按可靠度从高到低:

1. 中转注入的 `X-Customs-Trace` 请求头(网关可能剥掉)。
2. 响应头里的关联号:LiteLLM 的 `x-litellm-call-id`(已核实源码读取该响应头);
   `/spend/logs` 可用 `request_id` 或 `litellm_call_id` 查询(已核实)。
3. 网关自己的请求号:new-api 的 `request_id` 与 `upstream_request_id`(已核实字段);magpie 的 `rid`。
4. 兜底:时间窗口加模型名加输入 token 数,匹配不唯一就不合并,宁可分开显示。

### 5.2 存储设计

数据目录 `~/.customs-llm/`(可用 `--data` 改):

```
customs.db        SQLite(WAL 模式)
blobs/ab/cdef…    内容寻址的 blob 文件(按 SHA-256 命名)
overrides.json    临时测试条目清单(第 9 节)
config.toml       配置
daemon.json       端口与控制接口令牌(权限 0600)
```

**原则:**

1. **原始数据只追加,派生数据可重建。** Hop 和事件是真相;`ir` 结果、标记、摘要放在派生表里,带解析器版本号,可随时丢弃重算。
2. **大内容进 blob,不进 SQLite。** 请求体、响应体、头部和长事件内容按内容哈希存成文件,数据库只存哈希。对话历史每轮重复,哈希去重效果很好。blob 用标准库 gzip 压缩。
3. **单写者。** 所有写入经过一个 goroutine,批量提交到事务;队列有上限,满了丢弃并计数,绝不阻塞转发路径。

**表:**

| 表 | 内容 |
|---|---|
| `sources` | 来源:`id`、`kind`(relay、litellm、opencode……)、`name`、配置、游标(JSON)、状态 |
| `traces` | 一次用户请求:起止时间、客户端、模型、状态、总耗时、首字节、in / out / cache、标记(JSON)、摘要(JSON) |
| `hops` | 一跳:`trace_id`、`seq`、`source_id`、证据层级、`from`、`to`、起止时间、首字节时间、请求(方法、路径、头 blob、体 blob)、响应(状态、头 blob、体 blob)、`finish`、错误 blob、`raw_ref` |
| `events` | 流事件:`hop_id`、`idx`、相对时间偏移(微秒)、类型、内容 blob(小的内联) |
| `corr_keys` | 关联键 → `trace_id`(响应头里的调用号、网关请求号、中转注入的追踪头等) |
| `ir_cache` | `hop_id`、`ir_version`、解析结果(JSON);解析器升级后按版本号失效 |
| `probe_runs` | 一次探测:网关、驱动、起止时间、所用测试条目 |
| `check_results` | `run_id`、`case_id`、被测网关、结果、证据层级、关联的 Hop |
| `overrides` | 临时测试条目的记录(与 `overrides.json` 对应):网关、种类、引用、创建与删除时间 |

**索引:** `traces(started_at)`、`traces(status)`、`traces(model)`、`hops(trace_id, seq)`、`corr_keys(key)`。全文搜索(SQLite FTS5)后期再加。

**迁移:** 编号的 SQL 文件嵌入二进制,用 `PRAGMA user_version` 记录版本,启动时顺序执行。

**保留与清理:** 按时间和总大小两个上限;先删最旧的 trace,再对 blob 做标记清除(没有任何 Hop 引用的哈希才删除)。

**游标不放在文件里。** 采集器的游标(偏移量、inode、开头哈希、接口游标)存在 `sources` 表里,与它产生的 Hop 在同一个事务提交,崩溃后不会重复也不会漏。去重键是 `(source, 原始行哈希)`。

## 6. 各软件的日志、抓取和接入

"验证状态":**已核实**指我读了对应源码或文档;**未核实**指没有找到依据,实现前必须在真机上确认。

### 6.0 日志采集方式

每个采集器实现同一个接口:`Detect()` 找到来源,`Run()` 持续产出 Hop,游标(cursor)记录读到哪里。按来源类型分三种:

| 类型 | 做法 | 例子 |
|---|---|---|
| 追文件 | 记住偏移量,只读新增的行 | opencode 的 `opencode.log`、magpie 的 `app.log` |
| 轮询接口 | 带游标定时拉取新记录 | LiteLLM `/spend/logs/v2`、new-api 管理端日志、magpie `/v1/magpie/usage` |
| 读数据库 | 只读打开,按时间或主键增量读 | LiteLLM、new-api 的库,opencode 的会话库 |

追文件必须处理:

- **原地裁剪。** opencode 的日志超过 50 MiB 就从头部裁掉一半,偏移量会失效。游标同时记录 inode、文件大小和开头若干字节的哈希;发现变小或开头变了就重新定位,用"行时间戳 + 行哈希"去重。
- **轮转。** magpie 的 `app.log` 到 5 MiB 变成 `app.old.log`,两个文件都要看。
- **半行。** 只处理以换行结尾的行。
- **起点。** 默认从"现在"开始;历史回填只读最近一段(默认最近 1 小时或 10 MiB)。
- **监听。** 文件系统通知为主,定时扫描兜底。

定位:

- opencode:`$XDG_DATA_HOME/opencode/log/opencode.log`;未设置则 `~/.local/share/opencode/log/`。源码在各个系统上都这样取路径(包括 macOS 和 Windows)。已核实(`global-roots.ts`)。
- magpie:`magpie log --path` 给出应用日志路径;请求记录优先走 `/v1/magpie/usage` 接口。`usage.jsonl` 的具体路径未找到文档,未核实。
- 其他来源:`Detect()` 返回候选路径和置信度,用户可在设置里手动添加。

**日志能给多少要如实对待。** 日志层多半没有请求体:opencode 的日志是 `key=value` 事件;magpie 的用量记录明确不含提示词、回复和错误文本。
因此日志层提供状态、用量、耗时、错误、session;内容类的对账行(图片数、工具数等)在这些来源下显示"未知"。
内容类的行只能由中转、假上游、以及 LiteLLM 的 `proxy_server_request` 填充。

**解析器的写法。** 每个采集器先在真机上打开调试日志抓一批真实样本,存成夹具,再写解析器和测试。这也是第 14 节很多待核实项的落实办法。

### 6.1 LiteLLM(网关)

| 项 | 内容 | 状态 |
|---|---|---|
| 日志存储 | 数据库表 `LiteLLM_SpendLogs`:`request_id`、`call_type`、`model`、`model_group`、`custom_llm_provider`、`api_base`、`prompt_tokens`、`completion_tokens`、`startTime`、`endTime`、`completionStartTime`、`request_duration_ms`、`status`、`cache_hit`、`session_id`、`litellm_call_id`、`messages`、`response`、`proxy_server_request`、`metadata` | 已核实(`schema.prisma`) |
| 读取方式 | `GET /spend/logs`(只返回最近若干行,源码有截断警告),`GET /spend/logs/v2` 分页 | 已核实路由存在;参数和鉴权**未核实** |
| 有什么 | 入站请求(`proxy_server_request`)、响应、用量、耗时、首字节时间、实际使用的 `api_base` | 已核实字段 |
| 缺什么 | **网关改写后发往上游的请求体**。表里没有这一项 | 已核实 |
| 补缺口 | `--detailed_debug` 会打印发往上游的请求(来自 issue litellm#41913 的描述,测试里有同名字符串) | 路径**未核实** |
| 内容是否落库 | `messages`、`response` 是否有值取决于 `store_prompts_in_spend_logs`;默认值**未核实** | 部分 |
| 接入方式 | 客户端 → 我们的中转 → LiteLLM 原地址。不改 LiteLLM 配置 | — |
| 想看出站字节 | 新增一个测试模型,`api_base` 指向我们的假上游,不改已有条目(第 9 节) | 路由已核实,前提未核实 |
| 启动方式 | `litellm --config config.yaml --port 4000` | 惯用法,**未逐条核实** |

### 6.2 opencode(客户端)

| 项 | 内容 | 状态 |
|---|---|---|
| 日志位置 | 数据目录下的 `log/` 目录,单文件 `opencode.log`,所有进程共用、只追加;超过 50 MiB 就从头部裁到约 25 MiB | 已核实(`global.ts`、`logging.ts`)。另有 `local` 模式,文件名可能不同,**未核实** |
| 日志格式 | 每行 `key=value`:`timestamp level run message ...` 加上下文标注 | 已核实 |
| 日志里有什么 | 流开始、provider、model、session 等事件(来自 issue opencode#59872 的日志样例)。**不含请求体和响应体**这一点是推断 | 部分 |
| 提高详细度 | `OPENCODE_LOG_LEVEL=DEBUG`、`OPENCODE_PRINT_LOGS=1`、`--print-logs`、`--log-level` | 已核实(`cli.mdx`) |
| 接入方式 | 用环境变量 `OPENCODE_CONFIG_CONTENT`(内联 JSON 配置)只对本次进程覆盖 provider 的 `baseURL`,不改磁盘上的配置 | 变量已核实;覆盖 provider baseURL 的 JSON 形状**未核实** |
| 托管的 opencode Go | 服务端日志拿不到。只能靠中转记录客户端到服务的这一跳 | — |
| 会话存储 | 新版本用数据库(SQLite)存会话,有 `session_message` 表(来自 issue 报错信息) | 表结构**未核实** |

### 6.3 magpie(本地网关)

| 项 | 内容 | 状态 |
|---|---|---|
| 默认端口 | `127.0.0.1:3425`,由 `MAGPIE_ADDR` 控制 | 已核实 |
| 应用日志 | `magpie log --path` 给出路径:macOS `~/Library/Logs/magpie/app.log`,Windows `%LOCALAPPDATA%\magpie\logs\app.log`,Linux `$XDG_STATE_HOME/magpie/app.log`;5 MiB 轮转 | 已核实 |
| 请求用量记录 | `usage.jsonl`,每行:`t agent provider model in out cache_read cache_write reasoning ms ttft_ms first_text_ms flow_ms status cost effort session err rid`。**明确不含提示词、回复和错误文本** | 已核实(文档) |
| 读取方式 | `GET /v1/magpie/usage`;可 `POST /v1/magpie/usage/requests` 上报 | 已核实 |
| 缺什么 | 请求体、响应体 | 已核实(文档自述) |
| 补缺口 | magpie 插件中间件:`onRequest(body)`、`onProvider(body, ctx)`(每次尝试一个 provider 时的实际请求体)、`onResponse`、`onEvent`。我们可以提供一个插件把这些交给本地端口 | 已核实钩子存在;插件打包与加载方式**未实测** |
| 接入方式 | 客户端 → 我们的中转 → `127.0.0.1:3425`。想看出站字节则新增一个测试 provider(第 9 节),不改已有 provider | 已核实(文档) |
| 已知特点 | 配成只配 `url=` 时走"翻译档",配了 `anthropic=`/`responses=` 才是直通。两档丢失的信息不同 | 已核实(issue #4899) |

### 6.4 new-api(网关)

| 项 | 内容 | 状态 |
|---|---|---|
| 日志存储 | 表 `logs`:`request_id`、`upstream_request_id`、`user_id`、`token_name`、`model_name`、`channel`、`prompt_tokens`、`completion_tokens`、`use_time`、`is_stream`、`quota`、`type`、`content`、`other`(JSON 字符串)、`ip`、`created_at` | 已核实(`model/log.go`) |
| 有什么 | 用量、耗时、渠道、是否流式、两个请求号 | 已核实 |
| 缺什么 | 请求体和响应体在默认情况下**未发现**落库;`other` 里装什么需要看实例数据 | 未核实 |
| 读取方式 | 管理端日志 API(`controller/log.go` 存在),路由与鉴权**未核实**;或直接读日志库 | 部分 |
| 接入方式 | 前置中转。想看出站字节则新增一个测试渠道(第 9 节) | 渠道增删接口未核实 |
| 特点 | 官方 issue 模板要求报告者提供"直连 vs 经 new-api"的对比证据,正是本程序要产出的东西 | 已核实 |

### 6.5 claude-code-router(本地网关)

| 项 | 内容 | 状态 |
|---|---|---|
| 端口 | 模型网关默认 `127.0.0.1:3456`;Web 管理界面 `3458` | 已核实(README) |
| 日志 | 自带 Logs 页:已解析的 provider、model、状态、token、耗时、错误 | 已核实(README 描述) |
| 日志存储位置与格式 | **未找到文档** | 未核实 |
| 接入方式 | 前置中转 | — |

### 6.6 客户端侧指向中转的方式

目标是"只对这一次进程生效,不改磁盘配置":

| 客户端 | 方式 | 状态 |
|---|---|---|
| Claude Code | 环境变量 `ANTHROPIC_BASE_URL=http://127.0.0.1:9000/<目标名>` | 惯用法,本次**未核实** |
| Codex | `config.toml` 的 `[model_providers.*] base_url`;magpie 文档提到可用 Codex 自己的 `-c` 覆盖项 | 部分 |
| opencode | `OPENCODE_CONFIG_CONTENT`,见 6.2 | 部分 |
| 其他 | 若只能改配置文件,界面给出要改的那一行,由用户决定 | — |

## 7. 中转 relay

### 配置

`customs.toml`(程序启动时读取,也可全用命令行参数):

```toml
[relay]
listen = "127.0.0.1:9000"

[[target]]
name     = "litellm"
upstream = "http://127.0.0.1:4000"      # 你的网关原来的地址,不改它

[[target]]
name     = "magpie"
upstream = "http://127.0.0.1:3425"

[[target]]
name     = "opencode-go"
upstream = "https://opencode.ai/zen/go"  # 托管服务:只能记录客户端到它的这一跳
```

客户端把地址写成 `http://127.0.0.1:9000/litellm/v1/...`,中转去掉第一段 `litellm` 后转发到对应上游。

### 行为约定

- 请求体原样转发,不做任何改写,仅追加 `X-Customs-Trace`(可关)。
- 响应逐块转发并立即刷新;SSE 事件各自带到达时间。
- 记录首字节时间、完整耗时、连接被重置、上游无响应的时长。
- 上游失败时,把上游**原始的**状态码和响应体先记下来,再原样返回给客户端。
- 只监听回环地址。
- HTTPS 上游由中转自己发起 TLS,不需要用户信任任何证书。

### 启动与退出

- 程序启动时一起启动,端口占用就报错退出。
- 因为客户端是用环境变量指过来的,**关掉终端或退出本程序就恢复原状**,没有残留配置。

## 8. 展示

按用户真正会问的问题组织。这些问题来自对 issue 的调研:

1. 这次请求为什么失败/卡住?
2. 请求慢在哪一环?
3. 我发的东西,对面收到了吗(图片、system、工具定义、缓存标记)?
4. 缓存命中了吗,token 花在哪?
5. 模型为什么没调用工具?
6. 流是不是正常结束了?

### 8.1 列表页

每行一次请求,一行内能看出结论,不用点进去:

`时间 | 客户端 → 目标 | 模型 | 状态 | 总耗时 / 首字节 | in / out / cache | finish | 标记`

- 状态区分:成功、上游错误、连接重置、无响应超时、流未正常结束。
- 标记:来自第 12 节的检查项,例如"出站少 1 张图""cache 读写为 0""finish_reason 异常"。
- 筛选:失败、慢于阈值、有标记、按模型、按目标。

### 8.2 详情页

从上到下:

1. **一句话摘要**:发生了什么,用事实陈述。
2. **耗时瀑布图**:客户端 → 中转 → 网关 → 上游,每段的等待和传输,首字节位置,流中的停顿高亮。回答"慢在哪一环"。
3. **对账表**:每一跳收到的语义项计数并排对比。这是代替"直接看 JSON"的核心:

   | | 客户端发出 | 网关记录的入站 | 出站(若有) |
   |---|---|---|---|
   | system 消息 | 2 | 2 | 1 |
   | 用户/助手消息 | 8 | 8 | 8 |
   | 图片 | 1 | 1 | 0 |
   | 工具定义 | 12 | 12 | 12 |
   | tool_use / tool_result | 3 / 3 | 3 / 3 | 3 / 2 |
   | cache_control 标记 | 2 | 2 | 0 |
   | reasoning 参数 | high | high | — |

   不同的格子高亮,每个格子旁边显示证据层级(`wire` / `reported` / `inferred`)。没有数据的格子显示"未知",不猜。
4. **对话视图**:把消息渲染成对话。system 折叠;工具调用和结果配对显示;图片显示缩略图;长文本折叠。
5. **用量面板**:in / out / cache 读 / cache 写 / reasoning,直连与经网关的差异(有数据时)。
6. **流视图**:事件时间线,标出首个内容事件、终止事件、`[DONE]`、用量块是否出现。
7. **错误视图**:**先显示上游原始响应体**,再显示客户端最终看到的错误文案,二者并排,回答"错误是谁改写的"。
8. **检查结果**:第 12 节的检查项,每项链接回对应 issue。
9. **原始数据标签页**:JSON、原始日志行。

### 8.3 不做的展示

- 不默认显示整段 JSON。
- 不对生成文本做任何语义评价。

### 8.4 前端技术

- 内嵌进二进制的本地 Web 应用(Go `embed`),用浏览器打开,默认 `127.0.0.1:7070`。
- 框架倾向 Preact 或 Svelte 加 Vite,体积小、不依赖重型运行时。**最终选型在 M1 开始时确定。**
- 耗时瀑布图手写 SVG,不引入图表库。
- 先不做桌面壳:浏览器即可使用,也避开签名与公证问题。
- 暗色优先,支持浅色。键盘可操作:上下键切换请求,`Tab` 切换详情标签。

### 8.5 设计原则

1. **结论在前,JSON 在后。** 每个页面先回答"出了什么事",再让用户往下钻。
2. **证据徽章。** 每个数据旁带 `wire` / `reported` / `inferred` 徽章,不同来源的数据一眼可分。
3. **没有数据就写"未知"。** 不留空,不猜。
4. **渐进展开。** 默认折叠长内容,点开才加载;消息体按需从接口取,列表不带体。

### 8.6 页面

1. **首页(来源状态)**:第一次打开时最重要的一页。
   - 逐个列出来源:中转是否在运行、LiteLLM 是否已连接、opencode 日志是否找到。
   - 每个来源旁写"现在能看到什么 / 开启 X 能多看到什么",并提供一键操作(复制环境变量命令、启动测试模型)。
   - 对应"用户几乎什么都不用做"的目标。
2. **请求列表**:类似浏览器 Network 面板,见 8.1。实时更新,需要列表虚拟化。
3. **请求详情**:见 8.2。
4. **检查页**:一个矩阵,行是数据集用例,列是被测网关,格子是结果加证据层级;点格子看详情和对应 issue 链接。
   顶部"运行测试"按钮触发驱动。这页也是对外展示的截图素材。
5. **设置**:来源配置、端口、数据保留、清理遗留的测试条目。

### 8.7 实时更新

用 SSE 推送新增和变化的 Hop,不用轮询。流式请求进行中的事件也通过它推送,这样列表和详情页能看到"正在进行"的请求。

### 8.8 顶部常驻提示

本地记录含密钥与对话原文(第 3 节:现阶段不脱敏)。界面顶部常驻一行提示,并显示数据库文件路径。

### 8.9 对账表

行是 `ir` 的语义项,列是链路上的各跳,格子是"值 + 证据徽章"。行直接来自数据集的痛点,每行标出对应的 issue。

| 组 | 行(各跳的计数或形态) | 对应的 issue |
|---|---|---|
| 消息与内容 | system 条数(含非首位的);user/assistant 条数;图片、文件、文档块数;凭空多出的文本块 | portkey#1838、litellm#48813、new-api#7920、#8048、ccr#1678、litellm#43737 |
| 工具 | 工具定义数与非 function 类型数;`tool_choice` 形态(自动、必须、具名);`parallel_tool_calls`;schema 约束数(strict、const、空 properties、anyOf);tool_use 与 tool_result 配对数,其中 `is_error` 数、并行数 | litellm#39735、#41913、#24051、#44979、magpie#4945、ccr#1643 |
| 缓存 | `cache_control` 标记数(system / 消息 / 工具) | portkey#1579、litellm#43324、magpie#5125 |
| 推理 | `reasoning_effort` / thinking / `output_config.effort`;回放的推理块数 | litellm#49574、#44560、#45546 |
| 参数与格式 | 输出上限的字段名和值;`response_format`;采样参数 | magpie#5030、new-api#7626、magpie#5082 |
| 头与泄漏 | 元数据、自定义头、查询串;鉴权头种类;**出站新增的顶层字段名**;**入站顶层字段在出站消失的名单** | magpie#4899、#5021、litellm#46701、#43582、#49479 |
| 响应 | 上游状态与错误体 vs 客户端看到的;`finish_reason`;终止事件、`[DONE]`、用量块是否出现;流事件按类型计数(文本、推理、工具调用、拒绝) | opencode#51530、litellm#48721、new-api#7489、#7252、litellm#45235 |
| 用量 | in、out、cache 读、cache 写、reasoning | ccr#1587、portkey#858、litellm#46613 |

格子的规则:

- **四种差异**分别用颜色区分:少了(`n → 少于 n`)、多了(`0 → n`)、改形态(字符串变成块、字段改名)、一致。一致的行默认折叠为"N 项一致",只展开有差异的组。
- **没有数据显示"未知"**,不猜。
- **列随可用的跳出现。** 没有出站数据就没有"出站"列,并提示"启动测试模型可看到"。
- **下钻。** 点一行看具体项(哪一张图、哪个工具),再点跳到原始 JSON 的对应路径。
- **已知问题徽章。** 行旁显示"已知类似问题 N 条",点开列出数据集中对应的 issue。数据集每个用例需要增加一个字段指向对账表的行(`reconcile_row`),目前尚未添加。

第一版的限制:

- 没有"目标协议是否有对应字段"的能力表,所以只显示"少了",不判断是合理的有损还是 bug;判定交给数据集用例里的断言。
- 只解析 OpenAI Chat 和 Anthropic Messages 的行,其他协议显示"暂未解析"。

## 9. 临时覆盖:用一个测试模型测出站字节

目标:用户几乎什么都不用做。

### 9.1 做法

1. 在被测网关里**新增一个测试模型**(名字形如 `customs-probe-<随机>`),其上游地址指向本程序的假上游端口。不修改任何已有条目。
2. 本程序向这个测试模型发数据集里的用例;假上游记录网关实际发出的字节,并返回预设响应。
3. 测完删除测试条目。删除前把条目 ID 写入清单文件 `~/.customs-llm/overrides.json`,程序崩溃后下次启动先清理。

第一版**只创建一种测试模型**:走 OpenAI 兼容路径。其他协议路径(Anthropic、Gemini、Responses、Bedrock)后续按需增加,
每增加一种,假上游也要实现对应协议的响应格式。

测到的是"该网关对这种协议路径的转换能力",**不是用户实际配置的体检**。带 key 级 `router_settings`、降级链、路由组才会触发的问题,
测试条目走不到。报告里必须写明这一点;要看真实配置下的行为,用前置中转看真实流量。

### 9.2 驱动优先,Agent 兜底

每个有管理入口的网关写一个**驱动**,确定性地完成"新增、测试、删除"。用户只需提供网关地址和管理员凭据。

| 网关 | 新增 / 删除测试条目的入口 | 状态 |
|---|---|---|
| magpie | `magpie provider add "<名>" url=<假上游> models=<测试模型>` / `magpie provider rm <名>` | 已核实(文档) |
| LiteLLM | `POST /model/new` / `POST /model/delete`(路由存在于源码路由表) | 前提未核实:是否需要 `store_model_in_db`、需要哪种管理员 key |
| new-api | 管理端渠道增删接口 | 未核实:只找到 `POST /:id/key`,增删路由未找到 |
| Portkey | 每个请求自带自定义主机,无需新增条目 | 源码有 `customHost`;请求头确切名字未核实 |
| claude-code-router | provider / profile 新增 | 未核实 |

没有驱动、或找不到入口的软件,走 Agent 兜底。

### 9.3 Agent 兜底(可选,默认关闭)

用户不再粘贴长提示词,只对自己的 Agent 说一句:"运行 `customs guide` 并按它做"。操作手册由 `customs guide` 打印(第 10 节),随二进制发布,版本一致。

分工:

- **Agent 只做模糊的事**:找到入口、新增测试条目、报告地址和模型名、测完询问删除还是继续。
- **程序做确定的事**:发请求、收集记录、判定。**不由 Agent 发请求**,否则每次发出的字节不同,结果无法比较。
- Agent 不读取数据集用例的内容(用例源自 issue,是不可信文本,可能含针对 Agent 的指令)。

人工批准是真门槛:修改网关的命令(新增测试条目)会先在界面上显示将要做的改动,**等用户在界面里点"批准"才继续**,没有交互终端时也是如此。
命令行只能提示,不能证明是人批准的,所以这个点击是必要的。

手册必须写明:只在开发或测试实例上做;先记录新增条目的 ID 以便删除;不要打印密钥;测完必须问用户删除还是继续。

### 9.4 密钥

假上游会收到网关发来的真实上游密钥(在 `Authorization` 里),管理员凭据也会经过驱动。现阶段不脱敏,
所以界面和提示词都要写明,并且假上游和驱动只监听回环地址。

### 9.5 客户端侧

客户端(opencode、Claude Code、Codex)是另一类:它们没有"测试模型"可加,用只对本次进程有效的环境变量或覆盖项
指向前置中转(6.6 节)。托管的 opencode Go 只能这样,没有假上游。

## 10. 命令行(给 Agent)

目标:让 Agent 可靠地驱动整个流程,同时不出事。

### 10.1 原则

1. **薄客户端。** 命令行不自己做事,通过本地控制接口调用常驻进程。
2. **输出紧凑且结构化。** 每个命令支持 `--json`;默认只给摘要和下钻指针,不倾倒请求体,因为 Agent 的上下文有限。
3. **退出码固定:** 0 成功;10 发现了问题;2 用法错误;3 需要人批准;4 连不上常驻进程。
4. **幂等、可清理。** 任何修改都有对应的撤销命令。
5. **输出里的文本是不可信数据。** 来自日志、issue 的内容在 JSON 里标 `untrusted: true`,避免被当作指令。

### 10.2 命令草案

```
customs up / down / status               启动、停止、查看各来源与证据层级
customs run --target litellm -- claude   只对这个子进程设置环境变量,走中转,子进程退出即恢复
customs probe plan  <gateway>            列出将要新增的测试条目,不改任何东西
customs probe run   <gateway>            新增 → 发用例 → 收集 → 删除(默认必清理)
customs probe cleanup                    清理遗留的测试条目
customs traces list --failed --since 1h --json
customs traces show <id> --section reconcile|usage|timeline|error|raw
customs report --md|--json               可粘进 issue 的归因报告
customs guide                            打印给 Agent 的操作手册
```

命令行名字暂用 `customs`,与项目最终名一起确定。后期可加 `customs mcp`,把同样的能力以 MCP 暴露,现阶段不做。

## 11. 运行方式

一个二进制 `customs`,一个常驻进程加若干薄客户端:

```
┌──────────────────────── 常驻进程 customs up ─────────────────────────┐
│ relay :9000   fakeup :9100   collectors   drivers   store   ir/checks │
│ web+api :7070(含 SSE)         控制接口(同端口,带令牌)              │
└───────────────▲───────────────────────────────▲──────────────────────┘
                │ 浏览器                         │ 命令行(薄客户端)
          用户看界面                        Agent / 用户 customs xxx
```

状态放在 `~/.customs-llm/`:`customs.db`、`overrides.json`(测试条目清单)、`config.toml`、`daemon.json`(端口与令牌)。

启动顺序:`up` → 清理上次遗留的测试条目 → 起中转和假上游 → 起采集器 → 起前端接口。退出时按信号清理。

三种使用流程共用同一个进程:

- **A. 观察。** 用户 `customs up`,再 `customs run --target litellm -- opencode`(或自己设环境变量)。请求经中转记录,采集器同时读日志,浏览器里看列表和详情。
- **B. 用户自己测试。** 首页点"测这个网关":填地址和凭据 → 界面显示将新增的测试条目 → 批准 → 自动跑完 → 自动清理 → 在检查页看矩阵。
- **C. Agent 测试。** 用户对 Agent 说"运行 `customs guide`"。Agent 依次 `status`、`probe plan`,把计划展示给用户;用户在界面点批准;Agent `probe run`,再 `report`。

控制接口的令牌放在 `daemon.json`(权限 0600),命令行读取它;只监听回环地址。

## 12. 检查项

沿用 `testset/cases/`(独立成一个自包含的测试集,见 `testset/README.md`,新增用例不需要写 Go 代码)。查看器里每条检查项对应一条或多条用例,按证据层级给出结果:

- `wire` 层能判定的,直接给结论。
- 只有 `reported` 层的,标明"依据网关自述"。
- `detectable: no` 的用例(如 litellm#48813)在界面里列为"已知盲区",不参与判定。

`testset` 中全部用例目前 `verified: false`,在跑通执行器前,界面上的检查结果必须标注"规则来自 issue 描述,未实测"。

## 13. 里程碑

| 阶段 | 内容 | 产出 |
|---|---|---|
| M0 | 记录模型、SQLite 存储、关联键 | 能写入和查询 Hop 与 Trace |
| M1 | 中转 + `api` + 最小前端(请求列表、详情页的耗时瀑布、流视图、错误视图);同时确定前端框架 | 不依赖任何网关日志就能用,覆盖 opencode Go 这类托管服务 |
| M2 | 采集器:LiteLLM、opencode、magpie、new-api(按此顺序) | 补上 `reported` 层;每个采集器上线前先在真机核实 6 节里的"未核实"项 |
| M3 | `ir`(OpenAI Chat、Anthropic Messages)+ 对账表 + 检查项 + 首页 | 展示 issue 提炼的关键信息 |
| M4 | 假上游 + 临时覆盖驱动(magpie 先,LiteLLM 次之),一种测试模型 | 用户只给地址和凭据,拿到出站的精确字节 |
| M4.5 | 命令行(`probe`、`traces`、`report`、`guide`、`run`)与界面批准门槛 | Agent 可驱动整个流程,覆盖没有驱动的软件 |
| M5 | 重放与多目标并排对比 | 直连 vs 经网关的归因证据 |

## 14. 待核实清单

实现前必须逐项确认,不能当作已知:

1. LiteLLM:`/spend/logs/v2` 的参数与鉴权;`store_prompts_in_spend_logs` 默认值;`--detailed_debug` 打印出站请求的具体位置与格式。
2. opencode:`local` 模式的日志文件名;日志里是否真的没有请求体;`OPENCODE_CONFIG_CONTENT` 覆盖 provider `baseURL` 的 JSON 形状;会话 SQLite 的表结构。
3. magpie:插件中间件的打包与加载方式,能否把 `onProvider` 的请求体交给外部端口。
4. new-api:日志 API 的路由和鉴权;`other` 字段的实际内容;是否有开关保存请求体。
5. claude-code-router:日志存储位置和格式。
6. Claude Code、Codex 指向自定义地址的确切方式。
7. 跨平台:Windows 下环境变量覆盖和路径差异。
8. 临时覆盖:LiteLLM `/model/new` 的前提(`store_model_in_db`、管理员 key 类型);new-api 渠道增删接口;Portkey 自定义主机的请求头名;claude-code-router 的 provider 接口。
9. `ir` 各协议解析器的夹具来源:官方样例、数据集用例;解析不全时的回退行为。
10. 日志采集:各来源的真实样本夹具;opencode 日志是否含请求体;magpie `usage.jsonl` 的路径。
11. 控制接口的令牌机制与 Windows 下的实现。
12. 临时覆盖的清理:各网关删除条目后是否残留缓存或路由状态。

## 15. 命名

项目名定为 `customs-llm`:网关是边境,查看什么货物过了关、什么被扣、什么被夹带。
GitHub 上暂无同名项目(检查于 2026-11)。

- 仓库:`Vist233/customs-llm`(已在 GitHub 上创建,目前为空)。
- Go 模块路径:`github.com/Vist233/customs-llm`。
- 数据目录:`~/.customs-llm/`。
- 命令行命令:暂用 `customs`(短,易敲);也可以改成 `customs-llm`,待定。
