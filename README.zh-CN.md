# customs-llm

**customs-llm 显示一次 LLM 请求在每一跳实际发送和收到了什么,让你判断失败来自客户端、网关、上游 API 还是模型。** 它是一个本地程序,有网页界面和给 AI Agent 用的命令行,程序本身不含 AI。

[English](README.md) · [实测结果](docs/findings.md) · [设计文档](docs/design.md) · [测试集](testset/README.md)

> **想让 AI Agent 帮你排查?** 把这句话发给它:*"用 customs-llm(https://github.com/Vist233/customs-llm)找出我的 LLM 请求为什么失败。如果还没有安装 `customs`,先从最新 release 下载,然后运行 `customs guide` 并照着做。"*([详情](#怎么交给-ai-agent))

## 解决什么问题?

网关本应转发请求,却可能悄悄丢掉或改写内容,而症状表现为模型行为异常,不是报错。客户端也可能隐藏真正的原因。以下都是 2026-11-04 在 Docker 中实测的:

- LiteLLM 1.108.0 的 `pass_through_endpoints` 吞掉 JSON-RPC 的 `id`,对照字段保留([BerriAI/litellm#49479](https://github.com/BerriAI/litellm/issues/49479))。
- LiteLLM 1.108.0 发往 Mistral 时丢掉具名 `tool_choice`([BerriAI/litellm#39735](https://github.com/BerriAI/litellm/issues/39735))。
- magpie 0.1.2915 从 Anthropic Messages 转 OpenAI Chat 时丢掉 `cache_control`([yetone/magpie#5125](https://github.com/yetone/magpie/issues/5125))。
- opencode 1.19.5 遇到正文说明了额度问题的 429 时,40 秒内没有任何输出,一直在重试。中转能看到那段正文。

## 怎么运行?

到 [Releases](https://github.com/Vist233/customs-llm/releases) 下载:macOS 用 `.dmg`,Windows 用 `.exe`,Linux 用 `.tar.gz`,不需要安装 Go。打开后访问 <http://127.0.0.1:7070>,界面右上角可以切换中文。

让一个客户端经过中转,只对这个进程设置环境变量,不改配置文件:

```bash
customs run --target litellm -- 你的命令
```

## 怎么交给 AI Agent?

打开「托管」页,复制提示词,粘贴给 Claude Code、Codex 等 Agent。提示词里带有当前程序的路径、PID、端口和数据目录。命令行输出默认脱敏;修改网关必须由你在界面点批准。

## 局限

- FAIL 表示 issue 描述的行为出现,不一定是 bug。结论没有全部人工复核。
- new-api、Portkey、claude-code-router 还没跑过。
- 本地数据库和界面保存密钥和对话原文,请把数据目录当作敏感数据。
