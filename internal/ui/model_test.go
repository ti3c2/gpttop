package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"gpttop/internal/domain"
)

func TestNavigationDetailOutcomesAndHelp(t *testing.T) {
	m := NewModel(nil, Options{NoColor: true, InitialWidth: 120, InitialHeight: 40})
	m = updateModel(t, m, SnapshotMsg{Snapshot: testSnapshot(false)})
	if m.SelectedRow() != 0 {
		t.Fatalf("selected row = %d, want 0", m.SelectedRow())
	}
	m = press(t, m, "down")
	if m.SelectedRow() != 1 {
		t.Fatalf("selected row after down = %d, want 1", m.SelectedRow())
	}
	m = press(t, m, "k")
	if m.SelectedRow() != 0 {
		t.Fatalf("selected row after k = %d, want 0", m.SelectedRow())
	}
	m = press(t, m, "enter")
	if m.Mode() != ViewDetail {
		t.Fatalf("mode after enter = %v, want detail", m.Mode())
	}
	m = press(t, m, "j")
	if m.DetailOffset() != 1 {
		t.Fatalf("detail j should scroll, offset = %d", m.DetailOffset())
	}
	m = press(t, m, "o")
	if m.Mode() != ViewOutcomes {
		t.Fatalf("mode after o = %v, want outcomes", m.Mode())
	}
	m = press(t, m, "?")
	if !m.HelpVisible() {
		t.Fatalf("help was not visible")
	}
	view := m.View()
	for _, want := range []string{"gpttop | help", "outcomes", "q          quit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	for _, stale := range []string{"plot", "[/]"} {
		if strings.Contains(view, stale) {
			t.Fatalf("help contains stale plot binding %q:\n%s", stale, view)
		}
	}
	m = press(t, m, "esc")
	if m.HelpVisible() {
		t.Fatalf("esc should close help first")
	}
	m = press(t, m, "esc")
	if m.Mode() != ViewOverview {
		t.Fatalf("esc should return overview, got %v", m.Mode())
	}
}

func TestOutcomeWindowKeysAndPlotKeysNoop(t *testing.T) {
	m := NewModel(nil, Options{NoColor: true, InitialWidth: 100, InitialHeight: 30})
	m = updateModel(t, m, SnapshotMsg{Snapshot: testSnapshot(false)})
	m = press(t, m, "enter")
	if m.OutcomeWindow() != time.Minute {
		t.Fatalf("default outcome window = %s, want 1m", m.OutcomeWindow())
	}
	before := m
	for _, key := range []string{"p", "[", "]"} {
		m = press(t, m, key)
	}
	if m.Mode() != before.Mode() || m.OutcomeWindow() != before.OutcomeWindow() || m.DetailOffset() != before.DetailOffset() {
		t.Fatalf("plot keys changed hidden state: before=%#v after=%#v", before, m)
	}
	view := m.View()
	if strings.Contains(view, "plot") || strings.Contains(view, "[/]") {
		t.Fatalf("detail advertised plot behavior:\n%s", view)
	}
	m = press(t, m, "2")
	if m.OutcomeWindow() != 5*time.Minute {
		t.Fatalf("outcome window = %s, want 5m", m.OutcomeWindow())
	}
	m = press(t, m, "3")
	if m.OutcomeWindow() != 15*time.Minute {
		t.Fatalf("outcome window = %s, want 15m", m.OutcomeWindow())
	}
	m = press(t, m, "4")
	if m.OutcomeWindow() != domain.OutcomeAllTime {
		t.Fatalf("outcome window = %s, want all time", m.OutcomeWindow())
	}
	m = press(t, m, "o")
	view = m.View()
	for _, want := range []string{"window all", "1/2/3/4 window"} {
		if !strings.Contains(view, want) {
			t.Fatalf("all-time outcome view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "counts observed since gpttop started") {
		t.Fatalf("all-time outcome view contains removed explanatory text:\n%s", view)
	}
}

func TestOutcomeTitleMarksPartialWindow(t *testing.T) {
	snap := testSnapshot(false)
	window := 15 * time.Minute
	snap.Rows[0].EngineOutcomes[window][0].Count = domain.Value(42, "count", window, 5*time.Minute)
	m := NewModel(nil, Options{NoColor: true, InitialWidth: 120, InitialHeight: 40})
	m = updateModel(t, m, SnapshotMsg{Snapshot: snap})
	m = press(t, m, "o")
	m = press(t, m, "3")
	if view := m.View(); !strings.Contains(view, "window 15m~") {
		t.Fatalf("partial outcome window is not marked:\n%s", view)
	}

	snap.Rows[0].EngineOutcomes[window][0].Count = domain.Value(42, "count", window, window)
	m = updateModel(t, m, UpdateMsg{Snapshot: snap})
	if view := m.View(); strings.Contains(view, "window 15m~") {
		t.Fatalf("full outcome window is marked partial:\n%s", view)
	}
}

func TestGroupingToggleOrdersRowsAndPreservesSelection(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	snap := domain.AppSnapshot{
		At: now,
		Rows: []domain.ModelSnapshot{
			testRow(now, "z-endpoint", "alpha-model", domain.StateUP, false, false),
			testRow(now, "a-endpoint", "zeta-model", domain.StateUP, false, false),
		},
		NoColor: true,
	}
	m := NewModel(nil, Options{NoColor: true, InitialWidth: 120, InitialHeight: 40})
	m = updateModel(t, m, SnapshotMsg{Snapshot: snap})
	view := m.View()
	if strings.Index(view, "a-endpoint") > strings.Index(view, "z-endpoint") {
		t.Fatalf("default endpoint grouping did not order by endpoint:\n%s", view)
	}
	if got := m.selected(); got == nil || got.EndpointName != "a-endpoint" {
		t.Fatalf("default selected row = %#v, want a-endpoint", got)
	}
	m = press(t, m, "m")
	view = m.View()
	if strings.Index(view, "alpha-model") > strings.Index(view, "zeta-model") {
		t.Fatalf("model grouping did not order by model:\n%s", view)
	}
	if got := m.selected(); got == nil || got.EndpointName != "a-endpoint" || got.Model != "zeta-model" {
		t.Fatalf("group toggle did not preserve selected row identity: %#v", got)
	}
}

func TestOverviewHeadersUnitsAndAlignment(t *testing.T) {
	m := NewModel(nil, Options{NoColor: true, InitialWidth: 140, InitialHeight: 40})
	m = updateModel(t, m, SnapshotMsg{Snapshot: testSnapshot(false)})
	view := m.View()
	for _, want := range []string{"RPS(req/s)", "RUN(req)", "WAIT(req)", "PROMPT(tok/s)", "GEN(tok/s)", "KV(%)", "ERR(count)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("overview missing unit header %q:\n%s", want, view)
		}
	}
	for _, stale := range []string{"RUN/Q", "PROMPT/GEN"} {
		if strings.Contains(view, stale) {
			t.Fatalf("overview contains stale composite column %q:\n%s", stale, view)
		}
	}
	lines := strings.Split(view, "\n")
	data := firstLineContaining(lines, "gemma")
	if strings.Contains(data, "%") || strings.Contains(data, "/s") || strings.Contains(data, "tok/s") || strings.Contains(data, "cores") {
		t.Fatalf("overview data cell contains unit suffix:\n%s", data)
	}
	header := firstLineContaining(lines, "MODEL")
	for _, col := range []string{"RUN(req)", "WAIT(req)", "PROMPT(tok/s)", "GEN(tok/s)"} {
		if strings.Index(header, col) < 0 {
			t.Fatalf("header missing distinct column %q:\n%s", col, header)
		}
	}
}

func TestColoredAndNoColorRowsHaveSameVisibleGeometry(t *testing.T) {
	snap := testSnapshot(false)
	colored := NewModel(nil, Options{InitialWidth: 120, InitialHeight: 40})
	colored = updateModel(t, colored, SnapshotMsg{Snapshot: snap})
	noColorSnap := snap
	noColorSnap.NoColor = true
	plain := NewModel(nil, Options{InitialWidth: 120, InitialHeight: 40})
	plain = updateModel(t, plain, SnapshotMsg{Snapshot: noColorSnap})

	coloredLine := stripANSI(firstLineContaining(strings.Split(colored.View(), "\n"), "gemma"))
	plainLine := firstLineContaining(strings.Split(plain.View(), "\n"), "gemma")
	if coloredLine != plainLine {
		t.Fatalf("visible geometry differs:\ncolored=%q\nplain=%q", coloredLine, plainLine)
	}
}

func TestDetailExtremaNoMetricCursorAndScroll(t *testing.T) {
	snap := testSnapshot(false)
	snap.Rows[0].Metrics = manyMetrics(snap.Rows[0].Metrics, 20)
	m := NewModel(nil, Options{NoColor: true, InitialWidth: 100, InitialHeight: 10})
	m = updateModel(t, m, SnapshotMsg{Snapshot: snap})
	m = press(t, m, "enter")
	view := m.View()
	for _, want := range []string{"MIN 15m", "MAX 15m", "rows "} {
		if !strings.Contains(view, want) {
			t.Fatalf("detail missing %q:\n%s", want, view)
		}
	}
	for _, want := range []string{"RPS, req/s"} {
		if !strings.Contains(view, want) {
			t.Fatalf("detail missing unit-qualified metric %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"UNIT", "Requests running, req"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("detail contains unwanted unit display %q:\n%s", unwanted, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), ">") && strings.Contains(line, "Metric ") {
			t.Fatalf("detail metric row contains cursor:\n%s", view)
		}
	}
	if strings.Contains(view, "Extra metric 19") {
		t.Fatalf("last metric should not be visible before scrolling:\n%s", view)
	}
	m = press(t, m, "end")
	view = m.View()
	if !strings.Contains(view, "Extra metric 19") {
		t.Fatalf("short detail view did not scroll to final metric:\n%s", view)
	}
}

func TestDetailMetricNamesIncludeMeaningfulUnits(t *testing.T) {
	m := NewModel(nil, Options{NoColor: true, InitialWidth: 120, InitialHeight: 40})
	m = updateModel(t, m, SnapshotMsg{Snapshot: testSnapshot(false)})
	m = press(t, m, "enter")
	view := m.View()
	for _, want := range []string{"RPS, req/s", "KV-cache usage, %", "TTFT p95, s", "API CPU, cores"} {
		if !strings.Contains(view, want) {
			t.Fatalf("detail missing unit-qualified metric %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"UNIT", "Requests running, req", "API CPU (cores), cores"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("detail contains unwanted unit display %q:\n%s", unwanted, view)
		}
	}
}

func TestDetailMarksPartialWindowsInHeaders(t *testing.T) {
	snap := testSnapshot(false)
	for i := range snap.Rows[0].Metrics {
		metric := &snap.Rows[0].Metrics[i]
		metric.Now = domain.Value(metric.Now.Value, metric.Now.Unit, 10*time.Second, 2*time.Second)
		metric.Five = domain.Value(metric.Five.Value, metric.Five.Unit, 5*time.Minute, 2*time.Minute)
	}
	m := NewModel(nil, Options{NoColor: true, InitialWidth: 120, InitialHeight: 40})
	m = updateModel(t, m, SnapshotMsg{Snapshot: snap})
	m = press(t, m, "enter")
	view := m.View()
	for _, want := range []string{"5m~", "15m~", "MIN 15m~", "MAX 15m~", "partial columns are marked ~"} {
		if !strings.Contains(view, want) {
			t.Fatalf("detail missing partial column marker %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "NOW~") {
		t.Fatalf("detail should not mark NOW partial:\n%s", view)
	}
	row := firstLineContaining(strings.Split(view, "\n"), "RPS, req/s")
	for _, bloated := range []string{"3.5~", "2.7~", "2.4~", "1.2~", "8.4~"} {
		if strings.Contains(row, bloated) {
			t.Fatalf("detail value contains per-cell partial marker %q:\n%s", bloated, view)
		}
	}
}

func TestResizeStatesMissingMetricsAndNoColor(t *testing.T) {
	snap := testSnapshot(true)
	m := NewModel(nil, Options{InitialWidth: 60, InitialHeight: 12})
	m = updateModel(t, m, SnapshotMsg{Snapshot: snap})
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 54, Height: 10})
	view := m.View()
	for _, want := range []string{"[UP]", "[WARMING]", "[STALE]", "[DOWN]", "[UNSUPPORTED]", "-"} {
		if !strings.Contains(view, want) {
			t.Fatalf("narrow overview missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "\x1b[") {
		t.Fatalf("NO_COLOR/no-color view contains ANSI escape: %q", view)
	}
	assertViewFits(t, view, 54, 10)
}

func TestViewsFitRepresentativeSizes(t *testing.T) {
	for _, size := range []struct {
		width  int
		height int
	}{
		{60, 18},
		{80, 24},
		{100, 24},
		{120, 30},
		{140, 40},
		{180, 50},
	} {
		m := NewModel(nil, Options{InitialWidth: size.width, InitialHeight: size.height})
		m = updateModel(t, m, SnapshotMsg{Snapshot: testSnapshot(false)})
		assertViewFits(t, m.View(), size.width, size.height)
		m = press(t, m, "enter")
		assertViewFits(t, m.View(), size.width, size.height)
		m = press(t, m, "o")
		assertViewFits(t, m.View(), size.width, size.height)
		m = press(t, m, "?")
		assertViewFits(t, m.View(), size.width, size.height)
	}
}

func TestRefreshAndQuitCommands(t *testing.T) {
	refreshes := 0
	m := NewModel(nil, Options{
		NoColor: true,
		Refresh: func() tea.Cmd {
			refreshes++
			return func() tea.Msg {
				return OperationalMsg{Level: "info", Message: "refreshed"}
			}
		},
	})
	model, cmd := m.Update(keyMsg("r"))
	m = model.(Model)
	if refreshes != 1 {
		t.Fatalf("refresh callback count = %d, want 1", refreshes)
	}
	if cmd == nil {
		t.Fatalf("refresh should return callback command")
	}
	m = updateModel(t, m, cmd())
	if !strings.Contains(m.View(), "refreshed") {
		t.Fatalf("operational message missing from view:\n%s", m.View())
	}
	model, cmd = m.Update(keyMsg("q"))
	m = model.(Model)
	if !m.Quitting() || cmd == nil {
		t.Fatalf("quit should mark model and return command")
	}
}

func TestFitLinePreservesANSIAndWidth(t *testing.T) {
	colored := "\x1b[31m" + strings.Repeat("x", 80) + "\x1b[0m"
	fitted := fitLine(colored, 12)
	if lipgloss.Width(fitted) != 12 {
		t.Fatalf("fitted ANSI width = %d, want 12: %q", lipgloss.Width(fitted), fitted)
	}
	if hasBrokenCSI(fitted) {
		t.Fatalf("fitted line contains broken ANSI CSI sequence: %q", fitted)
	}
	snap := testSnapshot(false)
	snap.Rows[0].Model = strings.Repeat("very-long-model-name-", 6)
	m := NewModel(nil, Options{InitialWidth: 44, InitialHeight: 12})
	m = updateModel(t, m, SnapshotMsg{Snapshot: snap})
	view := m.View()
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 44 {
			t.Fatalf("line exceeds visible width: %d > 44: %q", lipgloss.Width(line), line)
		}
		if hasBrokenCSI(line) {
			t.Fatalf("line contains broken ANSI CSI sequence: %q", line)
		}
	}
}

func press(t *testing.T, m Model, key string) Model {
	t.Helper()
	return updateModel(t, m, keyMsg(key))
}

func updateModel(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	model, _ := m.Update(msg)
	next, ok := model.(Model)
	if !ok {
		t.Fatalf("model type = %T, want ui.Model", model)
	}
	return next
}

func keyMsg(key string) tea.KeyMsg {
	switch key {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

func hasBrokenCSI(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '\x1b' {
			continue
		}
		if i+1 >= len(s) || s[i+1] != '[' {
			continue
		}
		found := false
		for j := i + 2; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				found = true
				i = j
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func firstLineContaining(lines []string, needle string) string {
	for _, line := range lines {
		if strings.Contains(stripANSI(line), needle) {
			return stripANSI(line)
		}
	}
	return ""
}

func assertViewFits(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Fatalf("view height %d exceeds %d:\n%s", len(lines), height, view)
	}
	for _, line := range lines {
		if lipgloss.Width(line) > width {
			t.Fatalf("line exceeds visible width: %d > %d: %q\n%s", lipgloss.Width(line), width, line, view)
		}
		if hasBrokenCSI(line) {
			t.Fatalf("line contains broken ANSI CSI sequence: %q\n%s", line, view)
		}
	}
}

func testSnapshot(includeStates bool) domain.AppSnapshot {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	rows := []domain.ModelSnapshot{
		testRow(now, "gpu01", "gemma", domain.StateUP, false, false),
		testRow(now, "gpu02", "qwen", domain.StateDown, false, false),
	}
	if includeStates {
		rows = append(rows,
			testRow(now, "gpu03", "llama", domain.StateWarming, false, false),
			testRow(now, "gpu04", "mixtral", domain.StateStale, false, false),
			testRow(now, "gpu05", "legacy", domain.StateUnsupported, true, true),
		)
		rows[1].Overview.RPS = domain.Missing(0, "missing")
		rows[1].Metrics[0].Now = domain.Missing(0, "missing")
	}
	return domain.AppSnapshot{At: now, Rows: rows, NoColor: includeStates}
}

func testRow(now time.Time, endpoint, model string, state domain.State, unsupported, restarted bool) domain.ModelSnapshot {
	one := time.Minute
	five := 5 * time.Minute
	fifteen := 15 * time.Minute
	metricRows := []domain.MetricRow{
		{
			Key:        domain.SemanticCompletedRequestRate,
			Label:      "RPS",
			Kind:       domain.KindCounter,
			Unit:       "requests/s",
			Now:        domain.Value(3.5, "requests/s", 10*time.Second, 10*time.Second),
			OneMin:     domain.Value(3.2, "requests/s", one, one),
			Five:       domain.Value(2.7, "requests/s", five, five),
			Fifteen:    domain.Value(2.4, "requests/s", fifteen, five),
			MinFifteen: domain.Value(1.2, "requests/s", fifteen, five),
			MaxFifteen: domain.Value(8.4, "requests/s", fifteen, five),
		},
		{
			Key:        domain.SemanticKVCacheUsage,
			Label:      "KV-cache usage",
			Kind:       domain.KindGauge,
			Unit:       "%",
			Now:        domain.Value(80, "%", 0, 10*time.Second),
			OneMin:     domain.Value(72, "%", one, one),
			Five:       domain.Value(66, "%", five, five),
			Fifteen:    domain.Value(55, "%", fifteen, five),
			MinFifteen: domain.Value(42, "%", fifteen, five),
			MaxFifteen: domain.Value(93, "%", fifteen, five),
		},
		{
			Key:        domain.SemanticTTFTP95,
			Label:      "TTFT p95",
			Kind:       domain.KindHistogram,
			Unit:       "s",
			Now:        domain.GreaterThan(0.21, "s", 10*time.Second, 10*time.Second),
			OneMin:     domain.Value(0.24, "s", one, one),
			Five:       domain.Value(0.31, "s", five, five),
			Fifteen:    domain.Value(0.35, "s", fifteen, five),
			MinFifteen: domain.Value(0.12, "s", fifteen, five),
			MaxFifteen: domain.GreaterThan(1.48, "s", fifteen, five),
		},
		{
			Key:        domain.SemanticAPICPU,
			Label:      "API CPU (cores)",
			Kind:       domain.KindCounter,
			Unit:       "cores",
			Now:        domain.Value(1.25, "cores", 10*time.Second, 10*time.Second),
			OneMin:     domain.Value(1.18, "cores", one, one),
			Five:       domain.Value(1.09, "cores", five, five),
			Fifteen:    domain.Value(1.02, "cores", fifteen, five),
			MinFifteen: domain.Value(0.40, "cores", fifteen, five),
			MaxFifteen: domain.Value(2.80, "cores", fifteen, five),
		},
	}
	return domain.ModelSnapshot{
		EndpointName:        endpoint,
		EndpointURL:         "http://" + endpoint + ":8000",
		SanitizedURL:        "http://" + endpoint + ":8000/metrics",
		Model:               model,
		State:               state,
		LastSuccess:         now.Add(-2 * time.Second),
		SampleAge:           2 * time.Second,
		ObservationDuration: 20 * time.Minute,
		ScrapeDuration:      45 * time.Millisecond,
		ConsecutiveFailures: failureCount(state),
		LastError:           errorForState(state),
		Restarted:           restarted,
		Unsupported:         unsupported,
		Overview: domain.OverviewValues{
			RPS:              metricRows[0].Now,
			Running:          domain.Value(12, "requests", 0, 10*time.Second),
			Waiting:          domain.Value(1, "requests", 0, 10*time.Second),
			PromptTokens:     domain.Value(310, "tokens/s", 0, 10*time.Second),
			GenerationTokens: domain.Value(42, "tokens/s", 0, 10*time.Second),
			PrefixHitRate:    domain.Value(9.4, "%", 0, 10*time.Second),
			KVCache:          metricRows[1].Now,
			TTFTP95:          metricRows[2].Now,
			E2EP95:           domain.Value(7.2, "s", 0, 10*time.Second),
			Errors:           domain.Value(float64(failureCount(state)), "count", one, one),
		},
		Metrics: metricRows,
		EngineOutcomes: map[time.Duration][]domain.EngineOutcome{
			domain.OutcomeAllTime: {
				{Reason: "stop", Count: domain.Value(32400, "count", domain.OutcomeAllTime, 30*time.Minute)},
			},
			one: {
				{Reason: "stop", Count: domain.Value(1080, "count", one, one)},
				{Reason: "", Count: domain.Value(1, "count", one, one)},
			},
			5 * time.Minute: {
				{Reason: "stop", Count: domain.Value(5400, "count", 5*time.Minute, 5*time.Minute)},
			},
			15 * time.Minute: {
				{Reason: "stop", Count: domain.Value(16200, "count", 15*time.Minute, 15*time.Minute)},
			},
		},
		HTTPOutcomes: map[time.Duration][]domain.HTTPOutcome{
			domain.OutcomeAllTime: {
				{Status: "2xx", Method: "POST", Handler: "/v1/chat/completions", Count: domain.Value(32400, "count", domain.OutcomeAllTime, 30*time.Minute)},
			},
			one: {
				{Status: "2xx", Method: "POST", Handler: "/v1/chat/completions", Count: domain.Value(1080, "count", one, one)},
			},
		},
		Warnings: []string{},
	}
}

func manyMetrics(seed []domain.MetricRow, count int) []domain.MetricRow {
	out := append([]domain.MetricRow(nil), seed...)
	for i := 0; i < count; i++ {
		m := seed[0]
		m.Key = domain.Semantic("extra_metric")
		m.Label = "Extra metric " + formatInt(i)
		out = append(out, m)
	}
	return out
}

func formatInt(v int) string {
	if v == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for v > 0 {
		i--
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	return string(digits[i:])
}

func failureCount(state domain.State) int {
	if state == domain.StateDown {
		return 3
	}
	return 0
}

func errorForState(state domain.State) string {
	if state == domain.StateDown {
		return "connection refused"
	}
	return ""
}
