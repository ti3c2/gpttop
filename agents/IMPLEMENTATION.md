You are the primary coding agent. Implement the complete `gpttop` project in
this repository. Do not merely produce a plan, scaffold, pseudocode, or a mock
screen. Continue through implementation, integration, tests, executable build,
and documentation verification until the acceptance criteria are met.

`gpttop` is a greenfield Go terminal application that directly scrapes the
Prometheus/OpenMetrics `/metrics` endpoints of one or more vLLM deployments. It
shows a compact numeric overview, a detailed `now / 1m / 5m / 15m` view,
request outcomes, and 15-minute extrema for each detailed metric.

Do **not** fork, copy, vendor, shell out to, or depend on `llmtop` or any other
vLLM monitoring application. General-purpose Go libraries for Prometheus
parsing, YAML, CLI handling, and TUI rendering are allowed. The production
deliverable must be one self-contained executable and must not require vLLM,
Python, CUDA, Prometheus, Grafana, Node, Docker, a database, or a background
daemon on the client machine.

Treat the repository's `README.md` as the product contract. Keep it accurate as
the implementation evolves. If this prompt and the README disagree on a minor
detail, prefer the behavior that is more explicit and testable, then update both
the implementation-facing comments and README consistently.

## 1. Inspect, plan, and protect the workspace

Before editing:

1. Read all repository instructions, including every applicable `AGENTS.md`.
2. Inspect the repository layout, Git remotes, and `git status`.
3. Preserve all existing user work. Never reset, delete, or overwrite unrelated
   changes.
4. Determine the Go module path from the existing module or Git remote. If no
   canonical module path exists, use a valid local module name and keep the
   code easy to rename later.
5. Create a concrete plan and keep it updated while working.
6. Establish shared domain types, package interfaces, and the dependency list
   before asking multiple agents to build against them.

Do not ask the user routine implementation questions. Make reasonable,
documented choices inside the scope below. Ask only if a repository-specific
constraint makes the requested result materially ambiguous or impossible.

## 2. Mandatory subagent workflow

Use the available agent slots. The primary agent must actively coordinate
subagents rather than doing all work serially.

After defining shared contracts, spawn three subagents with non-overlapping
ownership:

### Subagent A: telemetry math

Owns:

```text
internal/prom/**
internal/history/**
internal/derive/**
internal/aggregate/**
internal/testdata/metrics/**
```

Responsibilities:

- Prometheus/OpenMetrics parsing and semantic metric aliases.
- Raw series identity and normalization.
- Bounded rolling history.
- Gauge averages, counter rates, counter ratios, resets, restarts, histogram
  deltas, p50/p95 quantiles, and replica aggregation.
- Deterministic unit and fuzz tests for all calculations.

### Subagent B: collection and configuration

Owns:

```text
internal/config/**
internal/scrape/**
internal/output/**
internal/testdata/config/**
```

Responsibilities:

- YAML configuration, repeatable endpoint flags, URL normalization,
  environment expansion, validation, and sanitization.
- Independent concurrent endpoint scraping, health state, timeouts, body
  limits, cancellation, and scripted `httptest.Server` integration tests.
- Non-interactive table and JSON presenters.

### Subagent C: terminal interface

Owns:

```text
internal/ui/**
```

Responsibilities:

- Bubble Tea application, overview, details, outcomes, help, resizing, and
  keyboard handling.
- Numeric detail tables, responsive width handling, and deterministic
  model/update/view tests.
- Demo data source and deterministic model/update/view tests.

### Primary agent ownership

The primary agent owns:

```text
internal/domain/**
internal/buildinfo/**
cmd/gpttop/**
go.mod
go.sum
Makefile
scripts/**
.github/workflows/**
README.md
```

The primary agent must:

- Define interfaces before delegation and communicate any later changes.
- Prevent simultaneous edits to the same file.
- Keep subagents from independently changing `go.mod` or `go.sum`; agents
  should report dependencies and the primary should add/tidy them centrally.
- Integrate the packages, own application lifecycle, and resolve mismatches.
- Continue useful local work while agents run.
- Inspect every agent's changes rather than assuming they are correct.

Each subagent must report changed files, exported contracts, tests run, and any
remaining risks.

After the first implementation wave, reuse the agents for a review wave:

1. Telemetry agent audits all UI labels and presenters for mathematical
   correctness and missing-data behavior.
2. TUI agent audits CLI usability, narrow-terminal rendering, and clean exit.
3. Collection/config agent audits clean builds, release artifacts, secret
   redaction, and README accuracy.
4. Assign fixes with explicit file ownership, integrate them, and rerun all
   checks.

Do not finish while any subagent has an unresolved material finding. If agent
slots or delegation tools are temporarily unavailable, continue implementing
locally and delegate the independent review work as soon as they become
available.

## 3. Product scope

### Direct data source

Scrape each configured vLLM deployment over HTTP. Given:

```text
http://127.0.0.1:8000
```

use:

```text
http://127.0.0.1:8000/metrics
```

URL behavior must be deterministic:

- If the URL path is empty or `/`, append `/metrics`.
- If `metrics_path` is configured, resolve that path against the base URL.
- If the supplied URL already has another explicit path, treat it as a full
  metrics URL rather than silently rewriting it.
- Preserve legitimate query parameters internally but redact sensitive query
  values in display and errors.

Scraping requirements:

- Scrape configured endpoints independently and concurrently.
- Permit at most one in-flight scrape per endpoint.
- A dead or slow endpoint must never block updates from healthy endpoints or
  freeze the TUI.
- Reuse HTTP connections and attach a request context to every scrape.
- Make interval and timeout configurable; default to 2s and 3s.
- Require a 2xx response.
- Support Prometheus text and OpenMetrics exposition.
- Bound response bodies to 32 MiB and return a clear sanitized error when the
  limit is exceeded.
- Always close response bodies.
- Never convert a failed scrape into zero-valued measurements.
- Retain the last successful sample, with its age, and mark it stale/down.
- Track scrape duration, last success, consecutive failures, and a concise
  sanitized error.
- Cleanly cancel goroutines on `q`, `Ctrl+C`, `SIGINT`, and `SIGTERM` and
  restore terminal state.

Use `github.com/prometheus/common/expfmt` and the Prometheus metric protobuf
model, or another proven compatible parser. Do not parse exposition with
regular expressions or ad hoc line splitting.

### Series identity: store before aggregating

Preserve relevant raw series before calculating endpoint/model rollups.

Stable series identity is:

```text
metric name + sorted labels
```

For histogram families, preserve bucket boundary `le` as part of the bucket
identity while grouping the corresponding histogram by all other labels.

Do not immediately collapse all engine series after parsing. Individual
series can reset or disappear independently; early aggregation can hide a
partial reset and produce bogus negative deltas or spikes.

Primary UI row identity is:

```text
configured endpoint + model_name
```

An endpoint may expose several models and engines. A changed `model_name` is a
new identity and must not inherit the old model's history.

Aggregation within one endpoint/model:

- Sum running and waiting gauges across compatible engine series.
- Sum request and token counter deltas across compatible series before
  dividing by time.
- Sum prefix hit and query deltas separately, then divide.
- Sum compatible histogram bucket deltas before calculating p50/p95.
- Use the maximum engine KV-cache usage for the endpoint/model overview; model
  details may additionally expose the mean.
- Associate `process_cpu_seconds_total` with the endpoint because it normally
  has no `model_name` label. Do not pretend it belongs to one model when an API
  process exposes multiple models.
- Keep separately configured endpoints separate by default. Model-grouped view
  is an explicit presentation mode, not silent merging.

Unknown families are ignored. Missing optional metrics display `-` or `N/A`,
never zero. A single missing family must not take down the endpoint.

## 4. Canonical metric catalog

Create one central semantic registry instead of scattering exact vLLM names
through derivation and UI code.

Support these current metric names:

| Semantic value | Prometheus source |
| --- | --- |
| Model | `model_name` label |
| Requests running | `vllm:num_requests_running` |
| Requests waiting | `vllm:num_requests_waiting` |
| Prompt throughput | `vllm:prompt_tokens_total` |
| Generation throughput | `vllm:generation_tokens_total` |
| Prefix cache queries | `vllm:prefix_cache_queries_total` |
| Prefix cache hits | `vllm:prefix_cache_hits_total` |
| KV cache usage | `vllm:kv_cache_usage_perc` |
| Completion outcomes | `vllm:request_success_total{finished_reason=...}` |
| HTTP outcomes | `http_requests_total{status,method,handler}` |
| API CPU | `process_cpu_seconds_total` |
| Process restart evidence | `process_start_time_seconds` |
| TTFT | `vllm:time_to_first_token_seconds` histogram |
| Inter-token latency | `vllm:inter_token_latency_seconds` histogram |
| E2E latency | `vllm:e2e_request_latency_seconds` histogram |
| Queue phase | `vllm:request_queue_time_seconds` histogram |
| Inference phase | `vllm:request_inference_time_seconds` histogram |
| Prefill phase | `vllm:request_prefill_time_seconds` histogram |
| Decode phase | `vllm:request_decode_time_seconds` histogram |
| Preemptions | `vllm:num_preemptions_total` |
| Corrupted requests | `vllm:corrupted_requests_total` when present |

Compatibility aliases must include:

- `vllm:gpu_cache_usage_perc` as a legacy KV-cache gauge.
- `vllm:gpu_prefix_cache_hit_rate` when only a legacy hit-rate gauge is
  available. Clearly mark that a legacy gauge cannot be reconstructed into the
  same counter-window semantics.
- `vllm:time_per_output_token_seconds` as a deprecated fallback for inter-token
  latency.

Prometheus client-generated `*_created` metrics are not measurement values and
must be ignored. Parser normalization must be tested because counter family
names may be represented with or without the exposition `_total` suffix by
different parser layers.

## 5. Exact rolling semantics

Every detail view contains:

```text
Metric | now | 1m | 5m | 15m | MIN 15m | MAX 15m
```

The detail table has no separate unit column. Meaningful units are appended to
the metric name, for example `TTFT p95, s`, `KV-cache usage, %`, and
`API CPU, cores`. Plain request gauges such as `Requests running` and
`Requests waiting` do not receive a redundant `req` suffix.

The first successful scrape establishes a baseline. It must never produce a
counter rate, ratio, outcome delta, or histogram percentile by dividing a
lifetime counter by process uptime.

### “Now”

- Gauge: newest successful value.
- Counter rate or ratio: valid observations inside `current_window`, default
  10s. During initial warm-up, use available post-baseline coverage and mark it
  partial.
- Histogram percentile: observations inside `current_window`, calculated from
  bucket deltas. It is not the most recent individual request.
- Outcome count: count observed inside `current_window`.

If no valid baseline or no histogram observations exist, show unavailable.

### 15-minute extrema

`MIN 15m` and `MAX 15m` are extrema over time of the same derived signal shown
in `now`. For every successful scrape timestamp in the final 15-minute horizon,
derive the metric using `current_window`, then take the minimum and maximum of
available finite candidates.

- Gauges use the endpoint/model aggregate at that successful scrape.
- Counter rates and API CPU use reset-safe current-window rates, never lifetime
  totals.
- Prefix-cache ratio uses current-window delta hits divided by delta queries;
  zero-query candidates are skipped.
- Histogram extrema use current-window quantiles from bucket deltas.
- Missing, reset-crossing, incompatible, no-observation, `NaN`, and infinite
  candidates are skipped.
- Short warm-up history marks extrema partial with the same `~` convention as
  other windows.

### Gauges

- `now` is the latest valid gauge.
- 1m/5m/15m are time-weighted means, not naive arithmetic means of scrapes.
- Use last-value-hold integration only across healthy sampling segments.
- Do not carry a sample through an arbitrarily long outage. Treat a gap longer
  than a documented threshold such as `2.5 * interval` as uncovered time.
- Track and expose coverage so a partially observed window is not represented
  as complete.

### Counters and rates

For each stable raw series, accumulate valid non-negative consecutive deltas:

```text
rate(window) = sum(valid counter deltas) / covered wall-clock seconds
```

- Use actual sample timestamps, not the nominal scrape interval.
- Zero delta is a valid zero rate.
- Insufficient baseline is unavailable.
- Sum series deltas before dividing by covered time.
- Never produce a negative rate.

Define the overview request rate as **completed inference RPS**:

```text
delta(sum(vllm:request_success_total across finished_reason)) / covered time
```

This is a completion rate, not a request-arrival rate. Keep HTTP counts/rates in
the outcome/detail layer so health probes and unrelated HTTP routes do not
pollute the primary inference RPS.

Prompt and generation throughput use their token counter deltas in tokens/s.

API CPU cores are:

```text
delta(process_cpu_seconds_total) / delta(wall time)
```

The value may legitimately exceed `1.0`. Label it `API CPU (cores)` and
document that it covers the process exporting `/metrics`, not necessarily all
engine/container processes.

### Prefix-cache ratio

For every interval/window:

```text
hit rate = sum(valid delta hits) / sum(valid delta queries)
```

Never calculate rolling hit rate as lifetime `hits_total / queries_total`, and
never average per-engine hit rates. If query delta is zero, display unavailable,
not `0%`.

### Histograms and quantiles

Treat classic histogram buckets as cumulative counters.

For each raw histogram series:

1. Preserve its ordered bucket schema.
2. Accumulate valid non-negative consecutive bucket deltas within the window.
3. If a bucket or `_count` resets, skip the ambiguous crossing interval and
   start a new histogram generation.
4. If bucket boundaries change, do not subtract incompatible schemas; start a
   new generation.
5. Merge compatible bucket deltas across engines only after reset handling.
6. Validate/correct cumulative monotonicity using Prometheus-compatible
   behavior.
7. Calculate the requested quantile once from the merged window distribution.

Required histogram outputs:

- Queue, inference, prefill, and decode: p95.
- TTFT: p50 and p95.
- ITL: p50 and p95.
- E2E latency: p50 and p95.

Never average previously calculated percentiles, and never average replica
percentiles.

Implement and test Prometheus-style interpolation:

- Locate the bucket containing rank `q * count`.
- Interpolate within finite buckets using the previous and current upper
  boundaries.
- Handle a quantile in `+Inf` honestly, returning a bounded representation such
  as `> previous_upper_bound` rather than fabricated precision.
- Return unavailable when the window has zero observations.
- Preserve the fact that histogram accuracy is limited by vLLM's bucket
  boundaries.

Phase p95 values are shown side by side and must not be stacked or added. The
p95 queue request is not necessarily the p95 decode request.

### Restarts, resets, and disappearance

Use a change in `process_start_time_seconds` as primary endpoint-restart
evidence. Negative counter or bucket changes are per-series fallback evidence.

When an endpoint restarts:

- Insert a discontinuity into in-memory history.
- Start counter and histogram generations from a new baseline.
- Do not bridge rates or quantiles across the restart.
- Make gauges immediately available from the new process.
- Mark dependent windows partial/warming and show a short restart indicator.

When only one counter series decreases, reset that series without invalidating
unrelated series. Removed series are missing, not zero. Expire vanished model
rows only after a small documented grace period.

### Windows and retention

Defaults:

```text
scrape interval: 2s
scrape timeout: 3s
current window: 10s
history retention: 20m
display windows: 1m, 5m, 15m
```

History must retain enough pre-boundary data to calculate the longest window.
Before a full window is available, calculate only over meaningful valid
coverage and mark the value/window as partial, for example:

```text
15m*  warming 03:42 / 15:00
```

Never present three minutes of data as a complete 15-minute statistic. Direct
history is intentionally in memory and disappears on exit.

## 6. Outcomes and error limitations

Provide a selected-window outcome view, switchable between 1m, 5m, and 15m.
Keep engine and HTTP layers separate because their counts can describe the same
request and must not be added into a fabricated total.

Engine finishes from `vllm:request_success_total`:

- `stop`: ordinary successful completion.
- `length`: successful completion that reached the output limit.
- `abort`: cancellation/disconnect; do not automatically label it a server
  error.
- Preserve and display unknown future `finished_reason` values.

HTTP outcomes from `http_requests_total`:

- Group window deltas by status, method, and handler.
- Accept both exact numeric codes and grouped labels such as `2xx`, `4xx`, and
  `5xx`.
- Show client and server failures separately.
- Do not count the monitor's `/metrics` scrapes as inference requests.

The standard vLLM metrics endpoint cannot reveal exact causes such as OOM,
timeout, invalid JSON, authentication failure, or arbitrary exception text.
When no bounded error label exists, say `reason unavailable`. Do not invent
causes, parse arbitrary log text in the first release, or assume raw exception
strings are safe metric labels.

If HTTP metrics are absent, show them as unavailable; never infer zero errors
from absence.

## 7. TUI behavior

Use maintained libraries such as Bubble Tea, Bubbles, and Lip Gloss. Keep the
interface compact and useful over SSH. Network work must happen outside the
Bubble Tea update/render loop and arrive through typed messages.

### Overview

One row per endpoint/model. A normal-width view resembles:

```text
MODEL             ENDPOINT      STATE  RPS  RUN  WAIT  PROMPT/s  GEN/s  HIT%  KV%  ERR
gemma-3-27b-it    gpu01:8000    UP     ...  ...  ...   ...       ...    ...   ...  ...
```

On wider terminals add CPU, TTFT p95, and E2E p95. On narrow terminals preserve
identity, state, queue, KV pressure, and errors, then hide lower-priority
columns without panicking or corrupting lines.

States include:

- `UP`
- `WARMING`
- `STALE`
- `DOWN`
- `UNSUPPORTED` when HTTP succeeds but no vLLM metric families are recognized

Color is supplemental, never the only signal. Respect `NO_COLOR` and
`--no-color`.

### Detail

Selecting a row and pressing Enter opens a numeric-first view with:

- Full model name.
- Endpoint name and sanitized metrics URL.
- State, last successful scrape, scrape duration, and latest sanitized error.
- Complete `now / 1m / 5m / 15m` metric table.
- Queue/inference/prefill/decode p95.
- TTFT, ITL, and E2E p50/p95.
- Preemption rate/count when available.
- Selected-window engine and HTTP outcome breakdowns.
- Clear warm-up, coverage, restart, and missing-metric explanations.

### Numeric detail layout

The detail screen is a passive numeric table. It must not expose a metric
cursor, selected-metric state, plot toggle, or hidden plot API. If the detail
body is taller than the terminal, `Up`/`Down` and `j`/`k` scroll the body and a
compact rows indicator shows the visible range.

### Required keys

```text
Up/Down or j/k   select overview row; scroll detail when needed
Enter            open details; from details return to overview
Esc              return to overview
1 / 5 / 0        select 1m / 5m / 15m outcome window
o                focus outcomes
m                toggle endpoint/model-grouped overview
r                request immediate non-overlapping refresh
?                help
q, Ctrl+C        quit safely
```

Do not print logs below the alternate-screen application. Surface operational
messages through application state.

## 8. CLI and configuration

Required examples:

```bash
gpttop --endpoint http://127.0.0.1:8000

gpttop \
  --endpoint local=http://127.0.0.1:8000 \
  --endpoint gpu02=http://10.0.0.22:8000/metrics

gpttop --config ./gpttop.yaml
gpttop --once --output table --endpoint http://127.0.0.1:8000
gpttop --once --output json --endpoint http://127.0.0.1:8000
gpttop --demo
```

Required flags:

```text
-e, --endpoint [NAME=]URL    repeatable
-c, --config PATH
-i, --interval DURATION
    --timeout DURATION
    --current-window DUR
    --history DURATION
    --once
    --sample-duration DUR
    --output table|json
    --demo
    --no-color
    --version
-h, --help
```

In `--once` mode, take two scrapes separated by `sample-duration` (default 2s)
so rates have a valid baseline. Do not initialize the alternate screen.
Return nonzero for invalid configuration or when no endpoint produced any
usable sample; include per-endpoint failures in JSON/table output.

Support YAML:

```yaml
refresh_interval: 2s
scrape_timeout: 3s
current_window: 10s
history: 20m
windows: [1m, 5m, 15m]

endpoints:
  - name: local-gemma
    url: http://127.0.0.1:8000
    metrics_path: /metrics
    model: google/gemma-3-27b-it

  - name: gpu02
    url: https://inference.example.net/metrics
    headers:
      Authorization: "Bearer ${gpttop_TOKEN}"
```

Expand `${ENV_VAR}` references at runtime and fail clearly when a required
variable is missing. Never print expanded header values. Do not add an insecure
TLS-skip option by default.

Precedence:

```text
explicit CLI global flags > config values > defaults
```

Repeated CLI endpoints are appended to configured endpoints, as documented in
the README. Validate unique endpoint names, supported durations, timeout versus
interval behavior, `history >= 15m`, and `current_window >= interval`.

Fail fast with useful usage when neither endpoints nor `--demo` are supplied.

Non-interactive JSON must use a documented stable structure with endpoint,
model, state, scrape metadata, metric values, window coverage/partial flags,
and outcomes. Missing values should be JSON `null`, not fabricated zeroes.

## 9. Package and dependency guidance

Use the README package boundaries or an equally clean equivalent:

```text
cmd/gpttop/
internal/domain/
internal/config/
internal/scrape/
internal/prom/
internal/history/
internal/derive/
internal/aggregate/
internal/ui/
internal/output/
internal/buildinfo/
internal/testdata/
```

Keep dependencies modest and pinned in `go.mod`/`go.sum`. Suitable choices:

- `github.com/prometheus/common/expfmt`
- `github.com/prometheus/client_model/go`
- `github.com/charmbracelet/bubbletea`
- `github.com/charmbracelet/bubbles`
- `github.com/charmbracelet/lipgloss`
- `gopkg.in/yaml.v3`
- Cobra/pflag only if it materially simplifies the CLI
- `go-cmp` or `testify` for tests, if helpful

Use the installed supported Go toolchain and declare Go 1.24 or newer unless an
existing repository constraint requires a different compatible minimum. Do not
introduce CGO. Do not use global mutable state. Inject clocks/tickers where
needed so time-dependent tests do not sleep.

## 10. Demo mode

`--demo` is a first-class offline QA path, not production mocking. It should
generate deterministic evolving metrics for at least two endpoints:

- One healthy deployment.
- One deployment that develops queue/KV pressure and exercises warnings,
  partial history, outcomes, and extrema.

Demo mode must use the same domain messages, history, derivation, aggregation,
and TUI paths as real scraping. Only the sample source is synthetic.

## 11. Tests

Tests must be deterministic, offline, and independent of a live vLLM server.

### Parser fixtures

Commit compact current and legacy fixtures covering:

- Colon-containing metric names.
- Scientific notation.
- `+Inf` buckets.
- Arbitrary label order and escaped label values.
- Multiple engines and models.
- Missing optional and unknown metrics.
- Numeric and grouped HTTP status labels.
- Malformed exposition.

If the supplied full metrics dump is available in the workspace, sanitize and
copy it into repository testdata as an additional fixture. Do not make tests
depend on a path outside the repository.

For that supplied dump, assert at least:

```text
model: google/gemma-3-27b-it
requests running: 132
requests waiting: 119
KV usage: 98.01810023575938%
TTFT histogram count: 7699
E2E histogram count: 7567
engine outcomes: stop=7567, length=0, abort=0
HTTP outcomes: 2xx POST /v1/chat/completions=7567, 4xx GET handler=none=2
process CPU counter is parsed
```

### Calculation tests

Cover exact expected values for:

- First-scrape baseline with no fake rate.
- Regular and irregular counter intervals.
- Valid zero rates.
- Gauge time-weighted averages.
- Long failed-scrape gaps and partial coverage.
- Prefix ratio from delta sums, including zero-query windows.
- Known histogram p50/p95 from bucket deltas.
- Empty histogram windows.
- Quantile landing in `+Inf`.
- Multi-engine histogram merge before quantile.
- Incompatible bucket schemas.
- Isolated series reset.
- Partial engine reset.
- Whole-process restart without a spike.
- Model replacement.
- Bounded retention eviction.

Fuzz the Prometheus normalization boundary and histogram quantile function so
malformed input cannot panic the process.

### Scraper tests

Use scripted `httptest.Server` instances for:

- Successful changing exposition.
- Authentication header without secret disclosure.
- Timeout and cancellation.
- Non-2xx response.
- Oversized response.
- Malformed body.
- Supported content-type variants.
- One unhealthy and one healthy endpoint concurrently.
- Forced refresh without overlap.
- Graceful shutdown and no goroutine leak.

### TUI tests

Feed synthetic typed messages into the TUI model. Cover:

- Overview/detail/outcome navigation.
- Outcome-window selection, detail scrolling, and absence of plot bindings.
- Help and quit behavior.
- Wide and narrow resize.
- Empty, warming, partial, up, stale, down, restarted, and unsupported states.
- Missing metrics.
- Disabled color and `NO_COLOR`.

Prefer semantic assertions to brittle full ANSI snapshots, but include a small
number of deterministic golden render tests if useful.

### CLI/output tests

Cover:

- Config/default/CLI precedence.
- Repeated endpoint merge and duplicate-name validation.
- Environment expansion and sanitization.
- URL normalization.
- `--once` table and JSON.
- `--help` and `--version`.
- Non-TTY behavior.

Run race tests for all concurrent code.

## 12. Build, installation, CI, and release

Commit `go.mod`, `go.sum`, `.gitignore`, and a root `Makefile`.

Required targets:

```text
help
fmt
fmt-check
vet
test
test-race
check
build
build-all
install
release
clean
```

`make build` must produce:

```text
bin/gpttop
```

using the equivalent of:

```bash
CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w \
    -X '<module>/internal/buildinfo.Version=$(VERSION)' \
    -X '<module>/internal/buildinfo.Commit=$(COMMIT)' \
    -X '<module>/internal/buildinfo.Date=$(DATE)'" \
  -o bin/gpttop ./cmd/gpttop
```

Adapt linker symbol paths to the real module and implementation. `--version`
must display version, commit, and build date.

`make build-all` must create raw static binaries under `dist/` for at least:

```text
linux/amd64
linux/arm64
darwin/amd64
darwin/arm64
```

`make release VERSION=v0.1.0` must package versioned `.tar.gz` archives and a
SHA-256 checksum manifest. A small POSIX `scripts/release.sh` called by the
Makefile is acceptable. Do not require GoReleaser for the basic release path.

`make install PREFIX="$HOME/.local"` must install to
`$HOME/.local/bin/gpttop`; default `PREFIX` is `/usr/local`.

`make check` is the local/CI deterministic quality gate and must include format
verification, `go vet`, tests, and a build. `make test-race` runs the race
detector separately if combining it into every `check` would be too slow.

Add CI that runs format verification, vet, unit/integration tests, race tests,
and a current-platform build on a clean checkout. Do not push, publish a
release, or mutate any external service.

The executable must not require Go or project files after compilation.

## 13. Documentation

Ensure `README.md` accurately contains:

- The problem and greenfield scope.
- Numeric-first overview/detail mockups.
- One- and multi-endpoint examples.
- YAML configuration.
- Key bindings.
- Exact canonical metric mapping and aliases.
- Type-aware `now / 1m / 5m / 15m` semantics.
- First-scrape baseline, resets, partial windows, and aggregation rules.
- Outcome/error limitations.
- API CPU limitation.
- In-memory-history limitation.
- Build, install, test, cross-build, and release commands.
- Honest non-goals.

Do not claim exact exception reasons, arrival RPS, whole-deployment CPU, or
live-server validation unless those capabilities were actually implemented and
verified.

## 14. Acceptance criteria

The work is complete only when all conditions hold:

1. Implementation is greenfield and contains no `llmtop` code or dependency.
2. `make build` creates one executable `bin/gpttop`.
3. `make check` passes.
4. `go test -race ./...` passes.
5. `make build-all` creates Linux and macOS amd64/arm64 binaries.
6. `make release VERSION=v0.1.0` creates archives and valid SHA-256 checksums.
7. `bin/gpttop --help`, `--version`, and `--demo` run successfully.
8. Repeated `--endpoint` flags and YAML endpoints work together as documented.
9. Multiple endpoints scrape concurrently and failures remain isolated.
10. Every requested metric appears or is explicitly unavailable.
11. `now / 1m / 5m / 15m` values obey metric-type-specific rules.
12. First scrape is baseline only; no lifetime counter is mislabeled as a rate.
13. Histogram p50/p95 comes from window bucket deltas, never lifetime buckets
    or averaged percentiles.
14. Prefix-cache rate comes from window delta hits divided by delta queries.
15. Multi-engine histograms are merged only after per-series reset handling.
16. No reset produces a negative or absurd rate/spike.
17. Partial windows and stale data are visibly marked.
18. No plot or chart UI/API remains; numeric tables are the primary view.
19. Outcome breakdown uses only available labels and never invents a reason.
20. Missing data is not rendered as zero.
21. TUI handles resize and exits without corrupting the terminal.
22. README matches actual flags, paths, calculations, and limitations.
23. `git diff --check` passes and unrelated user changes remain untouched.

## 15. Final verification and handoff

Before responding to the user, run and record the result of:

```bash
make fmt
make check
go test -race ./...
make build
./bin/gpttop --help
./bin/gpttop --version
make build-all
make release VERSION=v0.1.0
git diff --check
```

Also:

- Inspect release archive contents and validate the checksum manifest.
- Review the complete diff for secrets, unrelated edits, accidental build
  products, TODO placeholders, and stale documentation.
- Run demo mode in a real TTY when the environment supports it. If it does not,
  test the Bubble Tea model and say so honestly.
- Do not claim validation against a live vLLM deployment unless one was
  actually reachable and tested.

In the final response report:

- What was implemented.
- High-level architecture.
- Exact verification commands and pass/fail status.
- Paths to the local executable and release artifacts.
- Any optional metric families that were unavailable in fixtures.
- Honest remaining limitations of direct `/metrics` monitoring.

Do not stop early because no live endpoint exists. Fixtures, fake clocks,
`httptest.Server`, demo mode, and race tests must fully exercise the project.
