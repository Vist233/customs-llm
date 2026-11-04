# customs-llm

**customs-llm shows what an LLM request actually sent and received at every hop, so you can tell whether a failure came from your client, a gateway, the upstream API or the model.** It is a single local program with a web page and a command line built for AI agents. It contains no AI itself.

[中文说明](README.zh-CN.md) · [Measured results](docs/findings.md) · [Design](docs/design.md) · [Test set](testset/README.md)

## What problem does it solve?

An LLM gateway is supposed to forward your request, but it can silently drop or rewrite parts of it, and the failure shows up as odd model behaviour rather than an error. Clients can also hide the real cause. Both are measurable:

- In LiteLLM 1.108.0, a JSON-RPC body sent through `pass_through_endpoints` lost its `id` while a control field survived ([BerriAI/litellm#49479](https://github.com/BerriAI/litellm/issues/49479)).
- In LiteLLM 1.108.0, a named `tool_choice` to Mistral never reached the upstream ([BerriAI/litellm#39735](https://github.com/BerriAI/litellm/issues/39735)).
- In magpie 0.1.2915, `cache_control` was lost translating Anthropic Messages to OpenAI Chat ([yetone/magpie#5125](https://github.com/yetone/magpie/issues/5125)).
- opencode 1.19.5 printed nothing for 40 seconds while retrying a 429 whose body explained the quota problem. The relay shows that body.

Results come from running real software in Docker on 2026-11-04. See [docs/findings.md](docs/findings.md) for how, and for what was not tested.

## How does it work?

| Part | What it does | Evidence label |
|---|---|---|
| Relay | Forwards requests unchanged, streaming chunk by chunk, and records both sides | Seen on wire |
| Collectors | Read `opencode.db` and LiteLLM spend logs | Self-reported |
| Reconcile table | Compares hops item by item: messages, images, tools, `cache_control`, reasoning, usage, stream end | Per column |
| Probe | Sends 51 cases from real issues through a gateway to a fake upstream that records the exact bytes | Seen on wire |

It understands OpenAI Chat Completions, OpenAI Responses and Anthropic Messages.

## How do I run it?

Download the [latest release](https://github.com/Vist233/customs-llm/releases): `.dmg` for macOS, `.exe` for Windows, `.tar.gz` for Linux. No Go or other runtime is needed. Opening the app starts it and opens <http://127.0.0.1:7070>.

From the command line:

```bash
customs up --open --target litellm=http://127.0.0.1:4000
```

Send one client through the relay. This sets environment variables for that process only and changes no config file:

```bash
customs run --target litellm -- your-command
```

Build from source with Go 1.26 or newer:

```bash
go build -o customs ./cmd/customs
```

## How does an AI agent use it?

Open the **Agent** tab, copy the prompt and paste it into Claude Code, Codex or another agent. It includes the running program's path, process ID, ports and data directory. The agent then runs commands such as:

```bash
customs traces list --failed
```

```bash
customs traces show TRACE_ID --section error
```

```bash
customs report TRACE_ID
```

Output is redacted by default. Changing a gateway needs a person to click Approve in the web page. That guards against an agent approving its own plan by accident; it is not a security boundary against a local process.

## How do I test a gateway?

LiteLLM (with a database) and magpie have drivers. They add only temporary `customs-probe-*` entries and remove them after the run:

```bash
customs probe plan --driver litellm --url http://127.0.0.1:4000 --fake-url http://host.docker.internal:9100
```

```bash
customs probe run --plan PLAN_ID --key ADMIN_KEY --name litellm
```

| Gateway | PASS | FAIL | Inconclusive | Skipped |
|---|---|---|---|---|
| LiteLLM 1.108.0 | 20 | 21 | 4 | 6 |
| magpie 0.1.2915 | 20 | 9 | 7 | 15 |

FAIL means the behaviour described in the issue appears on that version. It is not always a bug: some are design choices, such as not forwarding custom headers. Verdicts were not all reviewed by hand. new-api, Portkey and claude-code-router have not been run.

## FAQ

**Why did my opencode request fail with no message?**
When the upstream returned 500, 429, 502 or dropped the connection, opencode 1.19.5 retried silently with growing delays instead of printing an error. An empty 400 printed only `Bad Request`. Send opencode through the relay to see the upstream's status and raw body. This reproduces the symptom with a fake upstream; it does not prove the cause of a particular failure.

**How do I see what LiteLLM sends upstream?**
LiteLLM's spend logs have no outbound request body. Point a temporary model at the fake upstream with `customs probe`, which records the exact bytes.

**Does it need a TLS certificate or a proxy setting?**
No. Clients use a different base URL for one process. It does not intercept TLS. Without changing a gateway, the hop from the gateway to the upstream is not visible.

**Is my data sent anywhere?**
No. It listens only on loopback and has no telemetry. The local database and this page keep API keys and conversation text in full, so treat the data directory as sensitive.

**Can it find every problem?**
No. A loss inside a provider is invisible at the gateway boundary, and it does not judge whether a model's answer is good.

## Development

```bash
go test ./...
```

CI runs on Linux, macOS and Windows. Pushing a `v*` tag builds every package and creates a draft release. Add a test case without writing Go: see [testset/README.md](testset/README.md).

## License

MIT, see [LICENSE](LICENSE). Bundled dependencies: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
