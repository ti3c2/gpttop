# gpttop numeric-first TUI rework

You are the primary coding agent for this focused follow-up. Implement the
changes completely; do not stop after a plan, mockup, or partial UI rewrite.
Carry the work through domain changes, derivation, rendering, tests,
documentation, formatting, linting, and a runnable demo.

Read the repository `AGENTS.md`, `README.md`, and `agents/IMPLEMENTATION.md`
before editing. `agents/IMPLEMENTATION.md` describes the original build, but
this document supersedes every requirement there that asks for terminal plots,
plot navigation, or a selectable metric cursor in the detail view.

Preserve unrelated user work. Inspect `git status` before editing and do not
reset or rewrite changes you did not make.

## Product decision

`gpttop` is a quick numeric examination tool. It is not a terminal graphing
package and should not try to compete with Prometheus/Grafana for historical
visualization.

This iteration must:

1. Remove plotting completely, including plot data contracts and JSON output.
2. Remove metric selection and the metric cursor from the detail screen.
3. Rework the TUI into a restrained, polished, information-dense interface.
4. Fix overview table alignment for colored and uncolored output.
5. Put overview units in column headers, never in data cells.
6. Add `MIN 15m` and `MAX 15m` values for every detail metric.

Do not change Prometheus scraping behavior, configuration semantics, metric
aliases, outcome classification, or the definitions of the existing `now`,
`1m`, `5m`, and `15m` columns except where required to compute the new extrema.

## Non-goals

- No plots, sparklines, Braille charts, block charts, or hidden plot API.
- No Grafana integration or Prometheus query backend.
- No new telemetry families.
- No rewrite of the scraper, history store, or CLI framework.
- No mouse-only interactions or Nerd Font dependency.
- No imitation of Lazygit's Git-specific multi-panel workflow. Borrow its
  hierarchy and polish, not its application structure.
- No broad dependency upgrade solely to restyle the interface.

## Current implementation findings

The implementation already identifies the important change points:

- `internal/ui/plot.go` is the terminal plot renderer.
- `internal/ui/model.go` owns `selectedMetric`, `plotEnabled`, `plotIndex`, and
  `plotWindow`, plus the `p`, `[`, and `]` bindings.
- `internal/ui/render.go` renders the metric cursor, plot, and plot help text.
- `internal/domain/types.go` exposes `PlotPoint`, `PlotSeries`, and
  `ModelSnapshot.Plots`.
- `internal/aggregate/snapshot.go` builds plot series after building metric
  rows.
- `internal/output/output.go` exposes plot data in JSON.
- `internal/ui/model_test.go` contains plot and metric-selection tests.
- `README.md` and `agents/IMPLEMENTATION.md` still promise plots.

The overview alignment bug is not subjective. `stateText` returns an ANSI
styled string, and that string is passed to `fmt.Sprintf` with a field width.
`fmt` counts escape bytes while the terminal counts visible cells, so every
column after `STATE` shifts when color is enabled. The current final-line
clipping then hides partial columns at common widths. Fix the table model; do
not compensate with more spaces or color-specific format strings.

## Research and design direction

Use these primary sources as design references:

- [Lazygit](https://github.com/jesseduffield/lazygit) demonstrates a clear
  active region, full-line selection, compact titles, strong information
  hierarchy, and a persistent contextual key line.
- [Lazygit UI configuration](https://github.com/jesseduffield/lazygit/blob/master/docs/Config.md)
  documents distinct active/inactive borders, selected-line styling, a bottom
  keybinding line, and automatic layout changes at terminal-size thresholds.
- [Lip Gloss v0.9.1](https://github.com/charmbracelet/lipgloss/tree/v0.9.1)
  provides terminal-cell width measurement, alignment, joining, borders, and a
  table package. The repository already depends on this version; use its
  available APIs or a small internal equivalent rather than copying examples
  from a newer incompatible major version.
- [Bubbles components](https://github.com/charmbracelet/bubbles) show two useful
  patterns even if no new dependency is added: tables own navigation/viewport
  behavior, and help text is generated from a central key map and truncates to
  the available width.
- [Bubble Tea v0.24.2](https://github.com/charmbracelet/bubbletea/tree/v0.24.2)
  reinforces keeping interaction state in the model, handling terminal events
  in `Update`, and rendering the complete interface from `View`.

Translate those references into `gpttop` as follows:

- Keep one dominant data surface instead of creating a dashboard of boxes.
- Use a compact title/status line, a clearly framed or separated data region,
  and a bottom contextual key line.
- Use one accent color for focus/selection, muted color for secondary metadata,
  and semantic green/yellow/red only for health state and warnings.
- Make structure survive `NO_COLOR`; color is never the only state signal.
- Highlight the selected overview row across its full visible width. Keep the
  overview row cursor because a row must be selected to open details.
- Do not highlight or prefix individual metric rows in the detail view.
- Reserve vertical space for the title/status and key lines. Do not build a
  large string and then blindly cut off the footer with `fitBlock`.
- Render `?` help as a dedicated overlay/view or replacement body, rather than
  appending it below content where a short terminal can hide it.

The intended character is operational and quiet: dense, aligned, easy to scan,
and polished over SSH. Avoid decorative gradients, excessive borders, large
logos, or color on every value.

## 1. Remove plotting completely

Delete plot behavior and contracts, not just the visible chart:

1. Delete `internal/ui/plot.go`.
2. Delete `PlotPoint`, `PlotSeries`, and `ModelSnapshot.Plots` from the domain
   package.
3. Delete `buildPlots`, `gaugePlot`, `kvPlot`, `counterPlot`, `ratioPlot`, and
   `histogramPlot` from aggregation.
4. Stop constructing plot data in `buildModelSnapshot`.
5. Delete plot JSON structs, conversion functions, and the `plots` row field.
   The JSON response must omit the key entirely; do not retain `"plots": []`.
6. Delete plot model state, accessors, clamping, synchronization, cycling, and
   rendering.
7. Remove the `p`, `[`, and `]` key bindings. They should be unbound/no-op and
   absent from the footer and full help.
8. Remove plot fixtures and plot-specific assertions from tests.
9. Remove `Options.DisableUnicode` if it has no non-plot use after the rewrite.
   Do not preserve dead API for hypothetical compatibility.
10. Remove every plot promise from `README.md` and update the stale plot
    requirements in `agents/IMPLEMENTATION.md` so a future agent is not told to
    add the feature back.

The `1`, `5`, and `0` keys still select the 1m, 5m, and 15m outcome window.
The current `plotWindow` happens to serve both plots and outcomes; replace it
with a clearly named `outcomeWindow` or `selectedOutcomeWindow`. Keep its
default at 1m and keep the selected window visible in the outcome title.

## 2. Remove detail metric selection

The detail cursor existed only to select a plotted metric. Remove:

- `selectedMetric` state and its public accessor.
- Metric-index clamping and plot synchronization.
- `>` prefixes and selected-row background styling on metric rows.
- Help text that says `j/k` selects a metric.

All metric rows in the detail table are passive numeric rows. `Enter` and
`Esc` continue to return to the overview, `o` opens outcomes, `r` refreshes,
`?` opens help, and `q` quits.

If the complete detail body is taller than the available content region, use a
plain vertical viewport/offset. In that case `Up`/`Down` and `j`/`k` scroll the
body without selecting or highlighting a metric; `PgUp`/`PgDown`, `Home`, and
`End` are desirable if they can be added cleanly. Show a small `rows x-y of z`
or equivalent scroll indicator only when content is actually clipped. A short
terminal must not silently make the final metric rows unreachable.

## 3. Define 15-minute extrema precisely

Add two fields to every `domain.MetricRow`:

```go
MinFifteen WindowValue
MaxFifteen WindowValue
```

Equivalent clear Go names are acceptable, but the JSON keys must be stable and
explicit:

```text
min_fifteen_min
max_fifteen_min
```

The detail column order is:

```text
METRIC | UNIT | NOW | 1m | 5m | 15m | MIN 15m | MAX 15m
```

`MIN 15m` and `MAX 15m` are extrema over time of the same derived signal shown
in `NOW`. They are not the min/max raw Prometheus sample and are not the min/max
of the four summary cells.

Use the latest successful sample time as the end of the 15-minute horizon. For
each successful sample timestamp in that horizon, derive the metric using the
normal current window (`current_window`, default 10s), then take the minimum
and maximum of the available derived values.

Metric-type rules:

| Kind | Candidate value used for extrema |
| --- | --- |
| Gauge | The endpoint/model aggregate at that scrape: summed running/waiting and max KV usage. Do not carry a gauge across a missing scrape just to create an extremum. |
| Counter rate | The reset-safe current-window rate ending at that scrape, including API CPU. Never compare lifetime counter totals. |
| Counter ratio | The current-window `sum(delta hits) / sum(delta queries)` ending at that scrape. Skip zero-query and unavailable windows. |
| Histogram quantile | The current-window quantile derived from bucket deltas ending at that scrape. Skip windows with no observations or incompatible/reset crossings. |

Additional rules:

- Reuse the existing per-series reset, restart, disappearance, histogram merge,
  and missing-data semantics. Do not create a second simplified math path.
- Skip unavailable, `NaN`, and infinite candidates.
- A zero candidate is valid and participates in min/max.
- Never insert zero for a missing sample or an outage.
- Valid candidates from either side of a process restart may participate, but
  no candidate may bridge the restart.
- When only a legacy prefix-hit gauge is available, derive candidates with the
  same legacy-gauge behavior as `NOW` and preserve the explanatory note.
- If no valid candidate exists, render `-` and serialize JSON `null`.
- If less than 15 minutes of usable history is available, calculate from the
  available post-baseline values and mark both extrema partial with the same
  `~` convention used by other windows. Do not present a short warm-up as a
  complete 15-minute range.
- Preserve the existing bounded `>x` representation for quantiles in the
  `+Inf` bucket. Do not fabricate a precise maximum.
- Set `Window` to 15m and propagate meaningful coverage/note metadata into the
  two `WindowValue` instances.

It is fine to build short-lived internal derived series to calculate extrema.
Do not reintroduce exported plot DTOs or serialize the series. Keep the work
bounded by retained history and avoid an obviously quadratic pass for every
render tick when the calculation can be shared per metric.

Add focused calculation tests for gauges, rates, ratios, and histogram
quantiles. Include warm-up, zero, missing data, reset/restart, and no-observation
cases. The expected numbers must be explicit, not assertions that merely check
availability.

## 4. Build one terminal-cell-aware table renderer

Replace the overview's independent narrow/wide `fmt.Sprintf` layouts with one
column specification and width-allocation path. Using the existing Lip Gloss
table package is acceptable if it meets all behavior below; a small internal
renderer is also acceptable.

A useful internal column model contains:

```text
id, header, unit, minimum width, preferred width, priority, alignment, value
```

Required behavior:

- Measure display width with `lipgloss.Width` or an equivalent ANSI-aware,
  Unicode-cell-aware function. Never use byte length for layout.
- Truncate by terminal cells, not bytes or runes alone.
- Pad/truncate the unstyled cell first, then apply style to the complete cell.
  ANSI escape sequences must never affect following columns.
- Left-align `MODEL`, `ENDPOINT`, and `STATE`.
- Right-align every numeric column, including headers/units consistently.
- Use one space between columns or a similarly restrained fixed gutter.
- Give headers and every row the exact same calculated column widths.
- Apply selection styling without changing the row's visible width.
- Do not use tab characters for the interactive table.
- Do not rely on final whole-line clipping to make the table fit. Hide complete
  low-priority columns and resize flexible identity columns before rendering.
- No line returned by `View` may exceed the current terminal width, with or
  without color.
- Long model and endpoint names must truncate with a visible marker while
  preserving the more useful prefix.

Split composite fields into real columns. Do not render `RUN/Q` or
`PROMPT/GEN`; use separate `RUN`, `WAIT`, `PROMPT`, and `GEN` cells so numbers
cannot drift within a compound string.

### Overview headers and units

The canonical overview columns are:

| Header | Unit in header | Cell example |
| --- | --- | --- |
| `MODEL` | none | `google/gemma-3-27b-it` |
| `ENDPOINT` | none | `gpu01` |
| `STATE` | none | `UP` |
| `RPS` | `req/s` | `4.5~` |
| `RUN` | `req` | `132` |
| `WAIT` | `req` | `119` |
| `PROMPT` | `tok/s` | `899.8~` |
| `GEN` | `tok/s` | `160.0~` |
| `HIT` | `%` | `45.0~` |
| `KV` | `%` | `98.0` |
| `CPU` | `cores` | `1.25~` |
| `TTFT95` | `s` | `0.21~` |
| `E2E95` | `s` | `7.20~` |
| `ERR` | `count` | `0~` |

Units may be rendered inline, such as `RPS (req/s)`, or as a second header row
directly below the labels. They must be visibly part of the column header. Data
cells must not contain `%`, `/s`, `t/s`, `ms`, `s`, `cores`, `requests`, or any
other unit suffix. The existing `~` partial marker, `>` bound marker, and `-`
missing marker are metadata, not units, and remain allowed.

Latency values on the overview use seconds because the header fixes the unit to
`s`; do not dynamically switch individual cells between milliseconds and
seconds. Keep numeric precision compact and consistent within each column.

Use a dedicated overview numeric formatter rather than weakening the general
`WindowValue` formatter used by other presenters.

### Responsive priorities

Derive visibility from the actual sum of calculated widths rather than a
single `width < 92` branch. Drop complete columns in reverse priority order.

Priority groups:

1. Always preserve `MODEL`, `STATE`, `RUN`, `WAIT`, `KV`, and `ERR` while the
   terminal is wide enough to show a useful table.
2. Preserve `RPS` next.
3. Add `ENDPOINT`, `PROMPT`, `GEN`, and `HIT` as space permits.
4. Add `CPU`, `TTFT95`, and `E2E95` on wide terminals.

Within a group, prefer dropping a lower-value column over truncating a numeric
header or cell. `MODEL` and `ENDPOINT` are the flexible columns. At extremely
small widths, render a deliberate compact fallback and a clear resize hint;
never render half a column or panic.

Test at representative widths around 60, 80, 100, 120, 140, and 180 cells.
Exact hard-coded breakpoints are not required; deterministic column selection
is.

## 5. Detail table presentation

Render details as a numeric table, not a selectable list:

```text
METRIC                 UNIT       NOW       1m       5m      15m   MIN 15m   MAX 15m
RPS                    req/s      3.7      3.6      3.3      3.1       1.2       8.4
Requests running       req        132      128      111       94        72       146
KV-cache usage         %         98.0     96.4     91.8     86.2      55.1      99.2
TTFT p95               s         0.21     0.24     0.31     0.35      0.12      1.48
```

Values are illustrative. Use actual derived data.

Required behavior:

- Put the metric unit in one `UNIT` cell and keep all numeric cells unit-free.
- Right-align numeric values and use consistent precision per unit.
- Render missing values as `-`, partial values with `~`, and bounded histogram
  values with `>`.
- Do not render a cursor column or selected metric background.
- Keep full model name, endpoint name, sanitized URL, health state, scrape age,
  duration, last success, failures, restart indicator, warnings, and sanitized
  error available above or below the table without turning them into one
  overflowing line.
- Wrap or truncate long metadata deliberately. Never allow it to push the
  table or footer past the viewport width.
- When width cannot fit all eight columns, preserve `METRIC`, `UNIT`, `NOW`,
  `MIN 15m`, and `MAX 15m` first, then add `1m`, `5m`, and `15m` as space
  permits. A user asking for extrema must not lose them at ordinary widths.

The overview selected-row status should be compact. Avoid repeating the full
title, URL, and every scrape field in multiple places. Show the selected
endpoint's error/warning prominently when present, because that is actionable.

## 6. Key handling and help

Use one source of truth for key bindings and their help labels so the footer,
full help, and `Update` behavior cannot drift.

Required keys after this change:

```text
Up/Down, j/k     select overview row; scroll detail when needed
Enter            open details; from details return to overview
Esc              return to overview or close help
o                open request outcomes
1 / 5 / 0        select 1m / 5m / 15m outcome window
m                toggle endpoint/model overview grouping
r                request an immediate non-overlapping refresh
?                toggle full help
q, Ctrl+C        quit and restore the terminal
```

Show only context-relevant bindings in the one-line footer. For example, plot
keys must not remain as stale text, and detail scroll keys need not be shown
when the complete detail table fits.

## 7. Color and visual hierarchy

Keep the palette small and terminal-safe:

- Accent: title, active border/separator, and selected overview row.
- Muted neutral: secondary timestamps, units, help descriptions, and inactive
  separators.
- Green: `UP` only.
- Yellow: `WARMING`, `STALE`, restart, and partial/warming notices.
- Red: `DOWN`, `UNSUPPORTED`, and current errors.

Do not color every numeric value. Do not use background color for health state
if it makes selected-row contrast unreadable. The selected row must remain
legible in both dark and light terminal themes supported by the current
dependency set.

With `--no-color` or `NO_COLOR`, emit no ANSI styling and retain explicit text
states such as `[UP]`, `[DOWN]`, `[RESTART]`, `~`, and `-`.

## 8. JSON and non-interactive output

Update JSON metric rows to include:

```json
{
  "key": "requests_running",
  "label": "Requests running",
  "kind": "gauge",
  "unit": "requests",
  "now": {},
  "one_min": {},
  "five_min": {},
  "fifteen_min": {},
  "min_fifteen_min": {},
  "max_fifteen_min": {}
}
```

This is structural illustration, not a license to serialize unavailable values
as empty objects. Preserve the current rule: unavailable `WindowValue` fields
serialize as JSON `null`.

Remove plot data from JSON entirely. Update JSON tests to prove both the new
extrema fields and the absence of the `plots` key.

The request for units-in-headers applies at minimum to the interactive overview.
Also make the one-shot table headers explicit about units where they are
currently ambiguous, as long as its stable data meaning is unchanged. Do not
add unit suffixes to one-shot numeric cells that would undermine alignment.

## 9. Tests

Replace plot-oriented tests with behavior-oriented tests.

### Aggregation tests

- Gauge min/max from known varying samples.
- Counter-rate min/max from known deltas and irregular timestamps.
- A valid zero rate as the minimum.
- Prefix-ratio min/max from delta sums, including a zero-query interval that is
  skipped rather than treated as zero.
- Histogram-quantile min/max from known bucket deltas.
- Counter reset and process restart do not create an extremum spike.
- Missing data and empty histogram windows remain unavailable.
- Less than 15 minutes of history produces values marked partial.
- Full 15-minute coverage is not marked partial.

### TUI tests

- Overview row navigation and selection preservation after snapshots/grouping.
- Detail and outcome navigation without metric selection state.
- `p`, `[`, and `]` do not enable any hidden behavior and are not advertised.
- `1`, `5`, and `0` still change the outcome window.
- Detail output contains `MIN 15m` and `MAX 15m` for every metric row.
- No detail metric row contains a cursor or selected-row styling.
- Short detail views can reach all metric rows through scrolling.
- Overview units appear in headers and do not appear in value cells.
- `RUN`, `WAIT`, `PROMPT`, and `GEN` are distinct columns.
- Numeric cells align under the same header across rows with different value
  lengths.
- ANSI-colored state/selection rendering has the same visible column positions
  as no-color rendering.
- Long Unicode model names, long endpoint names, missing values, partial values,
  and `>bound` values do not shift following columns.
- Every rendered line fits widths 60, 80, 100, 120, 140, and 180.
- Height-constrained views retain a visible title/status and footer.
- Help is readable at narrow widths and contains no plot bindings.
- `--no-color` and `NO_COLOR` emit no ANSI escapes.

Prefer semantic render assertions plus reusable helpers that inspect visible
cell positions. Do not replace everything with brittle full-screen snapshots.
A small number of golden views for 80x24 and 140x40 is useful if review remains
clear.

### Output tests

- JSON includes both extrema fields for each metric.
- Unavailable extrema are `null`.
- JSON contains no `plots` key.
- One-shot table headers name units and rows stay aligned.

## 10. Documentation

Update `README.md` to match the implementation:

- Describe `gpttop` as numeric-first, without plots.
- Remove plot feature bullets and plot key bindings.
- Update overview and detail examples with separate columns, header units, and
  `MIN 15m`/`MAX 15m`.
- Explain the extrema semantics in the metric-semantics section.
- Explain that durable graphs belong in Prometheus/Grafana and are outside this
  tool's scope.
- Change architecture/package descriptions that currently say terminal charts.
- Keep outcome window keys documented.

Update `agents/IMPLEMENTATION.md` consistently. Remove its plot section, plot
test requirements, plot key requirements, demo-plot language, and chart
acceptance criterion. Add the new numeric-table and extrema requirements so it
remains useful background rather than contradicting this task.

Do not claim validation against a live vLLM endpoint unless one was actually
used.

## 11. Likely file impact

Expected files include:

```text
internal/domain/types.go
internal/aggregate/snapshot.go
internal/aggregate/snapshot_test.go
internal/output/output.go
internal/output/output_test.go
internal/ui/model.go
internal/ui/render.go
internal/ui/model_test.go
internal/ui/plot.go                 # delete
cmd/gpttop/main.go                  # only if shared field checks require it
README.md
agents/IMPLEMENTATION.md
```

This is a guide, not permission to touch unrelated packages. Add small focused
files under the existing ownership boundaries when that makes table layout or
key maps easier to test.

## 12. Implementation order

1. Baseline `git status` and existing tests.
2. Remove plot contracts and rename the outcome-window state.
3. Add domain extrema fields and type-aware aggregation.
4. Update JSON/output contracts and tests.
5. Introduce the width-aware table/layout primitives.
6. Rebuild overview, detail, outcomes, help, and footer around those primitives.
7. Update navigation and scrolling tests.
8. Update README and the original implementation brief.
9. Run all verification and inspect the demo at several terminal sizes.

## 13. Acceptance criteria

The task is complete only when all of the following are true:

1. No terminal plotting code, plot model state, plot domain type, or plot JSON
   field remains.
2. `p`, `[`, and `]` are absent from behavior and help.
3. The detail table has no metric cursor or metric selection state.
4. Every detail metric has `NOW`, `1m`, `5m`, `15m`, `MIN 15m`, and `MAX 15m`.
5. Extrema use type-correct derived current-window values, not raw counter
   totals or fabricated zeroes.
6. Warm-up, reset, restart, gaps, and missing data remain honest.
7. Overview numeric cells contain no unit suffixes; headers state the units.
8. `RUN`, `WAIT`, `PROMPT`, and `GEN` are separate aligned columns.
9. Colored and no-color rows have identical visible column geometry.
10. Selected overview styling does not move columns.
11. Responsive layouts hide complete low-priority columns and never clip a
    partial column.
12. Every tested view fits its declared width and height, and short detail
    content remains reachable.
13. JSON includes extrema, excludes plots, and uses `null` for unavailable
    values.
14. README and `agents/IMPLEMENTATION.md` no longer contradict the product.
15. Existing scraping, derivation, outcome, CLI, and shutdown tests still pass.

## 14. Required verification

Run and report the results of:

```bash
make fmt
make lint
make test
make test-race
make build
make check
git diff --check
```

Then run `./bin/gpttop --demo` in a real PTY at approximately:

```text
80x24
120x30
140x40
180x50
```

Inspect overview, detail, outcomes, help, color, and no-color behavior. Confirm
that columns remain aligned after several live demo updates, not only on the
first warming frame. Quit normally and confirm the alternate screen and cursor
are restored.

In the final handoff, report changed files, the exact extrema semantics
implemented, tests/commands run, and any residual visual limitation. Do not
describe the task as complete if the demo was not visually inspected; say why
if the environment genuinely cannot provide a PTY.
