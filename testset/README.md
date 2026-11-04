# Test set

Fidelity cases mined from real issues in LLM gateway repos. Each case is one canonical request (and, for
response cases, a mock upstream reply) plus assertions about what must survive the gateway.

The test set is self-contained: adding a case needs no Go code. Loader and validation live in
[`internal/testset`](../internal/testset/testset.go); [`cmd/tsctl`](../cmd/tsctl/main.go) maintains it.

```
go run ./cmd/tsctl validate     # parse and validate every case
go run ./cmd/tsctl list
go run ./cmd/tsctl stats        # coverage by gateway, direction, category, severity, origin
go test ./...                   # also validates
```

## Layout

```
testset/cases/<gateway>/<id>.json
```

The directory name must equal `gateway.name`, and the file name must equal `id`. `id` is
`<gateway>-<issue>-<short-slug>`, lower-case, dashes only. One case per file; one issue may yield several cases.

## Fields

| field | meaning |
|---|---|
| `source` | the issue the case came from; always link back |
| `category` | kind of loss (see `categories` in `testset.go`) |
| `direction` | `request`, `response`, `stream` or `error`; non-request cases carry an `upstream` mock |
| `protocol` | `in` is what the client speaks, `out` is what the upstream speaks |
| `detectable` | `yes` / `partial` / `no`: is the loss visible in bytes crossing the gateway boundary |
| `payload_origin` | `issue` = copied from the issue, `reconstructed` = minimal payload we wrote |
| `verified` | `true` only after the case has been run against a real gateway |
| `preconditions` | gateway config needed to reproduce, when the bug depends on it |
| `examples` | optional recorded `bad` / `good` messages; assertions must fail on `bad` and pass on `good` |

## Assertions

Paths are a JSONPath subset (`$.a.b[0].c`). `[*]` means "any element": `present` passes if at least one
element has the path. `contains_anywhere` and `absent_anywhere` match text in the compact JSON of the whole
target message, which keeps cross-protocol cases format-agnostic. `ordered_text` requires its `texts` to appear
in that compact JSON in the given order; cases use unique markers (`ARG-A-MARKER`) so order is checkable without
parsing the protocol. `any_of` is for cases where several encodings of the same information are acceptable.

Targets: `upstream_request` (default for request cases; the final one if the gateway retries),
`upstream_headers`, `upstream_url`, `client_response` (default for other directions; streams are
`{"events":[...],"comments":[...]}`: events are the `data:` payloads parsed as JSON, comments are SSE comment
lines. In `upstream.sse`, an entry starting with `:` is a comment line.

## Status and caveats

- **Corner cases are marked in `notes`** (zero values, nested parts, ordering, absent versus zero, replayability).
- `verified: true` means the case produced PASS or FAIL on a real gateway (see `results/`), not that a person reviewed
  the verdict. Cases where the gateway has no matching route stay unverified.
- `examples` were recorded from a real LiteLLM run: `bad` when it failed, `good` when it passed. They guard against
  regressions in the checker.
- `payload_origin: reconstructed` means the issue gave no raw payload; the case uses a minimal one we wrote.
- `litellm-48813-oci-gemini-system-messages` is kept with `detectable: no`: the loss happens inside the
  provider, so a boundary checker cannot see it. It documents a blind spot.
- Some assertions are weak on purpose; each says so in `notes`.

## Adding a case

1. Pick an issue with a concrete payload or a clear before/after. Skip pure cost-tracking, logging and
   auth-config bugs: they are not visible in request or response bytes.
2. Copy a similar case under `testset/cases/<gateway>/` and edit it.
3. Prefer a payload from the issue; otherwise reconstruct the smallest one and mark it `reconstructed`.
4. Write assertions that state the invariant, not the current buggy output.
5. If the issue shows the bad (and a good) message, add `examples`.
6. `go run ./cmd/tsctl validate`.
