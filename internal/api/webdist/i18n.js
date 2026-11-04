"use strict";
// Interface language: English by default, Chinese on request. Chinese is the source text, so a missing entry shows Chinese.
const I18N_EN = {
  // navigation and layout
  "请求": "Requests", "检查": "Checks", "来源": "Sources", "托管": "Agent", "关闭": "Close",
  "本地记录含密钥与对话原文,仅保存在本机。命令行与导出默认脱敏;这里显示的是原始数据。": "Local records contain API keys and conversation text and stay on this machine. The CLI and exports redact them by default; this page shows raw data.",
  // states and evidence
  "成功": "OK", "HTTP 错误": "HTTP error", "连接失败": "Connection failed", "流未结束": "Stream incomplete", "进行中": "Running", "进行中…": "Running…",
  "亲眼所见": "seen on wire", "网关自述": "self-reported", "推断": "inferred", "证据来源": "Evidence source", "证据": "Evidence",
  // list
  "全部": "All", "失败": "Failed", "搜索模型 / 路径 / 客户端 / 目标": "Search model, path, client or target", " 条": " rows", "最新 ": "Latest ",
  "还没有记录。": "Nothing recorded yet.", "让客户端经过中转:": "Send a client through the relay:", "customs run --target NAME -- 你的命令": "customs run --target NAME -- your-command",
  ",或到「来源」页查看怎么接入。": ", or see the Sources page.", "时间": "Time", "状态": "Status", "目标": "Target", "模型": "Model",
  "耗时 / 首字节": "Total / first byte", "标记": "Flags", " <span class=\"muted\">流</span>": " <span class=\"muted\">stream</span>",
  // detail
  "← 返回列表": "← Back to list", "耗时": "Timing", "对账表": "Reconcile", "对话": "Conversation", "流": "Stream", "错误": "Error", "原始": "Raw", "状态码": "Status code",
  "请求还在进行中。": "The request is still running.", "上游返回 HTTP": "Upstream returned HTTP",
  "上游的原始错误体见「错误」标签,它可能和客户端最终显示的文案不同": "the raw error body is on the Error tab and may differ from what the client shows",
  "流式响应没有收到结束标记,可能被截断": "the stream never signalled its end and may be truncated", "首字节": "First byte", ",总计": ", total",
  "输入": "Input", "/ 输出": "/ output", "(缓存读取": "(cache read", "(缓存读取 0)": "(cache read 0)",
  "耗时瀑布图": "Timing waterfall", "等待上游": "Waiting", "接收响应": "Receiving", "流中有": "Stream has", "处停顿超过": "stalls longer than", "(红色)": "(red)",
  "只看有差异": "Only differences", "项一致已隐藏。": "identical rows hidden.",
  "只有一跳记录,先显示这一跳读到的内容。要对比出站字节,请启动测试模型(customs probe)或接入网关日志(来源页)。": "Only one hop recorded, so this shows what it contained. To compare outbound bytes, run a probe (customs probe) or connect gateway logs (Sources).",
  "没有数据的格子显示「未知」,不猜。": "Cells without data say Unknown; nothing is guessed.", "没有可显示的行。": "No rows to show.", "项": "Item",
  "已知问题": "Known issues", "测试集中的相关 issue:\n": "Related issues in the test set:\n", "<span class=\"muted\">未知</span>": "<span class=\"muted\">Unknown</span>",
  "(协议不同,不比较)": "(different protocols, not compared)",
  "图片": "Image", "↩ 工具结果": "↩ Tool result", "💭 推理": "💭 Reasoning", "块": "Block", "回复": "Reply", "没有数据": "No data",
  "无法按": "Cannot read", "未知协议": "unknown protocol", "解析为对话,请看「原始」。": "as a conversation; see Raw.",
  "这不是流式响应。": "This is not a streamed response.", "没有收到任何事件。": "No events received.", "+时间": "+Time", "间隔": "Gap", "类型": "Type", "内容": "Content",
  "<span class=\"badge b-warn\">注释</span>": "<span class=\"badge b-warn\">comment</span>",
  "这次请求没有错误。": "This request had no error.", "上游原始响应": "Raw upstream response", "传输层错误": "Transport error", "无": "None",
  "响应体大小": "Body size", "字节": "bytes", "<span class=\"stall\"> — 空正文,客户端只能显示一个笼统的错误</span>": "<span class=\"stall\"> — empty body, so the client can only show a generic error</span>",
  "原始响应体": "Raw body", "响应头": "Response headers", "这是中转亲眼看到的上游原始字节。客户端界面上显示的错误可能被改写过。": "These are the bytes the relay saw from upstream. The error shown in a client may have been rewritten.",
  "请求头": "Request headers", "请求体": "Request body", "响应体": "Response body", "(流式,事件见「流」)": " (streamed, see Stream for events)",
  // checks
  "保真度检查": "Fidelity checks", "每条用例来自真实 issue。": "Every case comes from a real issue.",
  "= 断言被违反,即 issue 描述的行为在这个网关版本上出现;": "= an assertion failed: the behaviour in the issue appears on this gateway version;",
  "= 网关保留了该保留的内容;": "= the gateway kept what it should;", "= 这次配置无法判定;": "= this setup cannot tell;",
  "= 无法运行。结果来自在 Docker 中对真实网关的运行(customs probe),不是推测。": "= could not run. Results come from running real gateways in Docker (customs probe), not from reasoning.",
  "搜索用例": "Search cases", "用例": "Case", "方向": "Direction", "严重度": "Severity", "未跑": "Not run", "<span class=\"muted\">未跑</span>": "<span class=\"muted\">Not run</span>",
  "现象": "Observed", "断言": "Assertions", "来自 issue": "from the issue", "按描述重建": "rebuilt from the description",
  ",已在真实网关上运行": ", run on a real gateway", ",未运行": ", not run", "备注": "Notes",
  // sources
  "中转": "Relay", "监听": "Listening", "数据目录": "Data directory", "版本": "Version", "名字": "Name", "上游": "Upstream", "让客户端用": "Point clients at",
  "<div class=\"muted\">还没有目标。</div>": "<div class=\"muted\">No targets yet.</div>", "名字,如 litellm": "Name, e.g. litellm", "上游,如 http://127.0.0.1:4000": "Upstream, e.g. http://127.0.0.1:4000",
  "添加目标": "Add target", "或在终端:": "Or in a terminal:", ",只对这一个子进程设置环境变量,退出即恢复,不改任何配置文件。": " sets environment variables for that one process only. No config file changes.",
  "数据来源": "Data sources", "说明": "Details",
  // plans
  "待批准:修改网关": "Awaiting approval: change gateway", "将新增": "Will add", "个临时测试条目,不改已有配置,跑完自动删除。请确认这不是生产环境。假上游会收到网关发来的请求。": "temporary test entries. Existing config is untouched and they are removed after the run. Make sure this is not production. The fake upstream will receive requests from the gateway.",
  "批准": "Approve", "拒绝": "Reject",
  // agent page
  "让 Agent 操作": "Let an agent operate this", "把下面这段粘贴给你的 Agent(Claude Code、Codex 等)。它会用命令行操作正在运行的这个程序。这段话不含密钥。": "Paste this into your agent (Claude Code, Codex and so on). It will operate this running program from the command line. The text contains no secrets.",
  "复制": "Copy", "已复制": "Copied", "Agent 能做的": "What the agent can do", "查看记录、读上游原始错误、生成脱敏报告、添加中转目标、让客户端经过中转。": "Read records and raw upstream errors, write redacted reports, add relay targets, and run clients through the relay.",
  "Agent 不能自己做的": "What it cannot do alone", "修改网关前必须由你在这里点批准。命令行输出默认脱敏,但界面和本地数据库保存原文。": "Changing a gateway needs your click in this UI. CLI output is redacted by default, but this UI and the local database keep raw data.",
  "运行信息": "Runtime details", "程序": "Program", "启动于": "Started", "连接信息文件": "Connection file", "界面和接口": "Web UI and API",
  "停止程序": "Stop program", "停止后中转不再可用,经过它的客户端会连不上。继续吗?": "After stopping, the relay is gone and clients using it cannot connect. Continue?", "程序已停止,可以关闭这个页面。": "The program has stopped. You can close this page.",
  // server-provided: reconcile rows
  "消息与内容": "Messages and content", "工具": "Tools", "缓存": "Cache", "推理": "Reasoning", "参数与格式": "Parameters and format", "头与泄漏": "Headers and leaks", "响应": "Response", "用量": "Usage",
  "system 消息数": "system messages", "developer 消息数": "developer messages", "user / assistant 消息数": "user / assistant messages", "图片块数": "image blocks", "文档/文件块数": "document / file blocks",
  "工具定义数": "tool definitions", "非 function 类型工具数": "non-function tools", "schema 约束(strict/const/anyOf/空 properties…)": "schema constraints (strict, const, anyOf, empty properties…)",
  "历史中的工具调用数": "tool calls in history", "历史中的工具结果数": "tool results in history", "其中 is_error 数": "of which is_error", "cache_control 标记数": "cache_control markers",
  "推理参数": "reasoning settings", "回放的推理块数": "replayed reasoning blocks", "输出上限(字段名=值)": "output limit (field=value)", "response_format / 结构化输出": "response_format / structured output",
  "采样参数": "sampling", "metadata 键": "metadata keys", "出站新增的顶层字段": "top-level fields added outbound", "入站顶层字段在出站消失": "top-level fields lost outbound",
  "error": "error", "finish / status": "finish / status", "返回的工具调用数": "tool calls returned", "流事件数": "stream events", "流:文本增量": "stream: text deltas", "流:推理增量": "stream: reasoning deltas",
  "流:工具调用事件": "stream: tool call events", "流:拒绝增量": "stream: refusal deltas", "流:收到终止事件": "stream: end event received", "流:[DONE]": "stream: [DONE]", "流:带用量": "stream: usage included",
  "输入 token": "input tokens", "输出 token": "output tokens", "缓存读取": "cache read", "缓存写入": "cache write", "推理 token": "reasoning tokens",
};

let LANG = "en";
try { if (localStorage.getItem("customs.lang") === "zh") LANG = "zh"; } catch {}
document.documentElement.lang = LANG === "zh" ? "zh" : "en";

function T(s) { return LANG === "zh" ? s : (I18N_EN[s] ?? s); }
function setLang(l) {
  try { localStorage.setItem("customs.lang", l); } catch {}
  location.reload();
}
// Status lines come from the daemon in Chinese, with numbers in them.
function tDetail(s) {
  if (LANG === "zh") return s;
  return s
    .replace(/^(\d+) 个目标$/, "$1 targets")
    .replace(/^还没有目标;添加后让客户端经过它$/, "No targets; add one and send a client through it")
    .replace(/^每 10 秒读取一次,已导入 (\d+) 条$/, "Polling every 10s, $1 imported")
    .replace(/^启动中,已导入 (\d+) 条$/, "Starting, $1 imported")
    .replace(/^未配置;用 --litellm-url 和 --litellm-key 开启$/, "Off; enable with --litellm-url and --litellm-key")
    .replace(/^未开启$/, "Off")
    .replace(/^没有找到 /, "Not found: ");
}
// Flags are stored in Chinese by the relay.
function tFlag(s) {
  if (LANG === "zh") return s;
  return s.replace(/^相同请求第 (\d+) 次$/, "Same request, attempt $1");
}
function bannerClosed() { try { return localStorage.getItem("customs.bannerClosed") === "1"; } catch { return false; } }
function closeBanner() { try { localStorage.setItem("customs.bannerClosed", "1"); } catch {} }
