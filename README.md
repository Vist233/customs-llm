# customs-llm

看清一次 LLM 请求在每一跳发生了什么,回答"问题出在哪一环"。本地运行,程序里没有 AI。

- **看见:** 本地中转原样转发并记录请求、响应、耗时和上游原始错误;同时读取 opencode 和 LiteLLM 自己的记录。每条数据标明证据来源:亲眼所见、网关自述、推断。
- **对账:** 把各跳收到的内容按语义项并排比较,支持 OpenAI Chat、OpenAI Responses、Anthropic Messages。
- **测试:** 用 [`testset/`](testset/README.md) 里来自真实 issue 的用例测网关,假上游记录网关实际发出的字节。

当前是开发版本,还没有打包发布。实测结果见 [docs/findings.md](docs/findings.md),设计见 [docs/design.md](docs/design.md)。

## 运行

需要 Go 编译一次(发布版不需要)。Go 1.26 或更新。

```bash
go build -o customs ./cmd/customs
```

```bash
./customs up --target litellm=http://127.0.0.1:4000
```

浏览器打开 <http://127.0.0.1:7070>。默认只监听本机。

让一个客户端经过中转,不改任何配置文件,环境变量只对这个子进程生效:

```bash
./customs run --target litellm -- 你的命令
```

看记录:

```bash
./customs traces list --failed
```

```bash
./customs traces show ID --section error
```

```bash
./customs report ID
```

给 AI Agent 用:让它运行 `customs guide` 并照做。命令行输出默认脱敏,界面和本地数据库保存原文。

## 测网关

驱动支持 LiteLLM(需要数据库)和 magpie:只新增 `customs-probe-` 前缀的临时条目,跑完删除。修改网关前必须在界面点批准。

```bash
./customs probe plan --driver litellm --url http://127.0.0.1:4000 --fake-url http://host.docker.internal:9100
```

```bash
./customs probe run --plan ID --key 网关管理员KEY --name litellm
```

在 Docker 里复现仓库中的结果:[`gateways/litellm/probe.sh`](gateways/litellm/probe.sh)、[`gateways/magpie/run.sh`](gateways/magpie/run.sh)、[`gateways/opencode/repro.sh`](gateways/opencode/repro.sh)。

## 开发

```bash
go test ./...
```
