package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"gpttop/internal/domain"
)

type styles struct {
	noColor       bool
	selected      lipgloss.Style
	muted         lipgloss.Style
	good          lipgloss.Style
	warn          lipgloss.Style
	bad           lipgloss.Style
	header        lipgloss.Style
	title         lipgloss.Style
	separator     lipgloss.Style
	section       lipgloss.Style
	outcomeWindow lipgloss.Style
}

type keyBinding struct {
	Keys     string
	Action   string
	Overview bool
	Detail   bool
	Outcomes bool
	Help     bool
	Scroll   bool
}

const screenBodyStartY = 2

var keyBindings = []keyBinding{
	{Keys: "j/k", Action: "select row", Overview: true},
	{Keys: "click", Action: "select row", Overview: true},
	{Keys: "j/k", Action: "select metric", Detail: true},
	{Keys: "click", Action: "select metric", Detail: true},
	{Keys: "pgup/pgdn", Action: "page metric", Detail: true},
	{Keys: "home/end", Action: "first/last metric", Detail: true},
	{Keys: "enter", Action: "detail", Overview: true, Outcomes: true},
	{Keys: "enter", Action: "overview", Detail: true},
	{Keys: "esc", Action: "overview", Detail: true, Outcomes: true},
	{Keys: "esc", Action: "close help", Help: true},
	{Keys: "o", Action: "outcomes", Overview: true, Detail: true},
	{Keys: "1/2/3/4", Action: "outcome window", Detail: true, Outcomes: true},
	{Keys: "m", Action: "group", Overview: true},
	{Keys: "r", Action: "refresh", Overview: true, Detail: true, Outcomes: true},
	{Keys: "?", Action: "help", Overview: true, Detail: true, Outcomes: true, Help: true},
	{Keys: "q", Action: "quit", Overview: true, Detail: true, Outcomes: true, Help: true},
}

func newStyles(noColor bool) styles {
	s := styles{noColor: noColor}
	if noColor {
		return s
	}
	lipgloss.SetColorProfile(termenv.ANSI256)
	s.selected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255")).Background(lipgloss.Color("60"))
	s.muted = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	s.good = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	s.warn = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	s.bad = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	s.header = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	s.title = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	s.separator = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	s.section = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
	s.outcomeWindow = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255")).Background(lipgloss.Color("236"))
	return s
}

func render(m Model) string {
	noColor := m.snapshot.NoColor || m.opts.NoColor
	st := newStyles(noColor)
	if m.showHelp {
		return composeScreen("gpttop | help", helpLines(m, st), footerLine(m, false), m.width, m.height, st, 0, false)
	}
	switch m.mode {
	case ViewDetail:
		title, body, scrollable := detailScreen(m, st)
		return composeScreen(title, body, footerLine(m, scrollable), m.width, m.height, st, m.detailOffset, true)
	case ViewOutcomes:
		return composeScreen(outcomesTitle(m), outcomesBody(m, st), footerLine(m, false), m.width, m.height, st, 0, false)
	default:
		return composeScreen(overviewTitle(m), overviewBody(m, st), footerLine(m, false), m.width, m.height, st, 0, false)
	}
}

func composeScreen(title string, body []string, footer string, width, height int, st styles, scrollOffset int, showScroll bool) string {
	if width <= 0 {
		width = 100
	}
	if height == 1 {
		return fitLine(st.title.Render(title), width)
	}
	if height == 2 {
		return strings.Join([]string{
			fitLine(st.title.Render(title), width),
			fitLine(st.muted.Render(footer), width),
		}, "\n")
	}
	separator := st.separator.Render(strings.Repeat("-", width))
	lines := []string{
		fitLine(st.title.Render(title), width),
		fitLine(separator, width),
	}
	bodyLimit := len(body)
	if height > 0 {
		bodyLimit = height - 3
		if bodyLimit < 0 {
			bodyLimit = 0
		}
	}
	body = fitBodyLines(body, width)
	if bodyLimit >= 0 && len(body) > bodyLimit {
		if showScroll && bodyLimit > 1 {
			visibleLimit := bodyLimit - 1
			maxOffset := len(body) - visibleLimit
			if scrollOffset < 0 {
				scrollOffset = 0
			}
			if scrollOffset > maxOffset {
				scrollOffset = maxOffset
			}
			end := scrollOffset + visibleLimit
			visible := append([]string(nil), body[scrollOffset:end]...)
			indicator := fmt.Sprintf("rows %d-%d of %d", scrollOffset+1, end, len(body))
			visible = append(visible, fitLine(st.muted.Render(indicator), width))
			body = visible
		} else {
			body = body[:bodyLimit]
		}
	}
	lines = append(lines, body...)
	lines = append(lines, fitLine(st.muted.Render(footer), width))
	return strings.Join(lines, "\n")
}

func fitBodyLines(lines []string, width int) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = fitLine(line, width)
	}
	return out
}

func overviewTitle(m Model) string {
	group := "endpoint"
	if m.groupByModel {
		group = "model"
	}
	return fmt.Sprintf("gpttop | overview | group %s | rows %d | %s", group, len(m.snapshot.Rows), formatClock(m.snapshot.At))
}

func overviewBody(m Model, st styles) []string {
	if !m.ready {
		return []string{"waiting for snapshot"}
	}
	if len(m.snapshot.Rows) == 0 {
		return []string{"no endpoints"}
	}
	lines := overviewTable(m, st)
	lines = append(lines, selectedStatusLines(m, st)...)
	return lines
}

func overviewTable(m Model, st styles) []string {
	columns := []tableColumn{
		{ID: "sel", Header: " ", MinWidth: 1, PreferredWidth: 1, Priority: 0, Align: alignLeft},
		{ID: "model", Header: "MODEL", MinWidth: 12, PreferredWidth: 28, Priority: 0, Align: alignLeft, Flexible: true},
		{ID: "endpoint", Header: "ENDPOINT", MinWidth: 10, PreferredWidth: 18, Priority: 2, DropOrder: 0, Align: alignLeft, Flexible: true},
		{ID: "state", Header: "STATE", MinWidth: 13, PreferredWidth: 18, Priority: 0, Align: alignLeft},
		{ID: "rps", Header: "RPS", Unit: "req/s", MinWidth: 10, PreferredWidth: 10, Priority: 1, Align: alignRight},
		{ID: "run", Header: "RUN", Unit: "req", MinWidth: 8, PreferredWidth: 8, Priority: 0, Align: alignRight},
		{ID: "wait", Header: "WAIT", Unit: "req", MinWidth: 9, PreferredWidth: 9, Priority: 0, Align: alignRight},
		{ID: "prompt", Header: "PROMPT", Unit: "tok/s", MinWidth: 13, PreferredWidth: 13, Priority: 2, DropOrder: 1, Align: alignRight},
		{ID: "gen", Header: "GEN", Unit: "tok/s", MinWidth: 10, PreferredWidth: 10, Priority: 2, DropOrder: 2, Align: alignRight},
		{ID: "hit", Header: "HIT", Unit: "%", MinWidth: 6, PreferredWidth: 7, Priority: 2, DropOrder: 3, Align: alignRight},
		{ID: "kv", Header: "KV", Unit: "%", MinWidth: 5, PreferredWidth: 7, Priority: 0, Align: alignRight},
		{ID: "cpu", Header: "CPU", Unit: "cores", MinWidth: 10, PreferredWidth: 10, Priority: 3, DropOrder: 1, Align: alignRight},
		{ID: "ttft95", Header: "TTFT95", Unit: "s", MinWidth: 9, PreferredWidth: 9, Priority: 3, DropOrder: 2, Align: alignRight},
		{ID: "e2e95", Header: "E2E95", Unit: "s", MinWidth: 8, PreferredWidth: 8, Priority: 3, DropOrder: 3, Align: alignRight},
		{ID: "err", Header: "ERR", Unit: "count", MinWidth: 10, PreferredWidth: 10, Priority: 0, Align: alignRight},
	}
	order := m.rowOrder()
	rows := make([][]tableCell, 0, len(order))
	for displayIndex, rowIndex := range order {
		row := m.snapshot.Rows[rowIndex]
		selector := " "
		if displayIndex == m.selectedRow {
			selector = ">"
		}
		rows = append(rows, []tableCell{
			{Text: selector},
			{Text: displayModel(row)},
			{Text: displayEndpoint(row)},
			{Text: stateLabel(row), Style: stateStyle(row, st)},
			{Text: formatOverviewValue(row.Overview.RPS)},
			{Text: formatOverviewValue(row.Overview.Running)},
			{Text: formatOverviewValue(row.Overview.Waiting)},
			{Text: formatOverviewValue(row.Overview.PromptTokens)},
			{Text: formatOverviewValue(row.Overview.GenerationTokens)},
			{Text: formatOverviewValue(row.Overview.PrefixHitRate)},
			{Text: formatOverviewValue(row.Overview.KVCache)},
			{Text: formatOverviewValue(row.Overview.APICPU)},
			{Text: formatOverviewValue(row.Overview.TTFTP95)},
			{Text: formatOverviewValue(row.Overview.E2EP95)},
			{Text: formatOverviewValue(row.Overview.Errors)},
		})
	}
	lines, ok := renderCellTableWithOptions(columns, rows, m.width, st, tableOptions{
		SelectedRow: m.selectedRow,
	})
	if ok {
		return lines
	}
	return overviewCompact(m, st)
}

func overviewCompact(m Model, st styles) []string {
	order := m.rowOrder()
	lines := []string{"resize wider for table"}
	for displayIndex, rowIndex := range order {
		row := m.snapshot.Rows[rowIndex]
		selector := " "
		if displayIndex == m.selectedRow {
			selector = ">"
		}
		line := fmt.Sprintf("%s %s %s run %s wait %s kv %s err %s",
			selector,
			displayModel(row),
			stateLabel(row),
			formatOverviewValue(row.Overview.Running),
			formatOverviewValue(row.Overview.Waiting),
			formatOverviewValue(row.Overview.KVCache),
			formatOverviewValue(row.Overview.Errors),
		)
		line = fitLine(line, m.width)
		if !st.noColor {
			line = padVisible(line, m.width)
			if displayIndex == m.selectedRow {
				line = st.selected.Render(line)
			}
		}
		lines = append(lines, line)
	}
	return lines
}

func selectedStatusLines(m Model, st styles) []string {
	row := m.selected()
	if row == nil {
		return nil
	}
	lines := []string{""}
	if m.width > 0 && m.width < 100 {
		lines = append(lines, fmt.Sprintf("selected %s @ %s  %s  age %s  failures %d",
			displayModel(*row),
			displayEndpoint(*row),
			stateText(*row, st),
			formatDuration(row.SampleAge),
			row.ConsecutiveFailures,
		))
	} else {
		lines = append(lines, fmt.Sprintf("selected %s @ %s  %s  age %s  scrape %s  last %s  failures %d",
			displayModel(*row),
			displayEndpoint(*row),
			stateText(*row, st),
			formatDuration(row.SampleAge),
			formatDuration(row.ScrapeDuration),
			formatClock(row.LastSuccess),
			row.ConsecutiveFailures,
		))
	}
	if row.LastError != "" {
		lines = append(lines, "error "+row.LastError)
	}
	if len(row.Warnings) > 0 {
		lines = append(lines, "warnings "+strings.Join(row.Warnings, "; "))
	}
	return lines
}

func detailScreen(m Model, st styles) (string, []string, bool) {
	row := m.selected()
	if row == nil {
		return overviewTitle(m), overviewBody(m, st), false
	}
	title := fmt.Sprintf("gpttop | detail | %s @ %s", displayModel(*row), displayEndpoint(*row))
	lines := []string{
		fmt.Sprintf("model %s", displayModel(*row)),
		fmt.Sprintf("endpoint %s  url %s", displayEndpoint(*row), displayURL(*row)),
		fmt.Sprintf("state %s  age %s  scrape %s  last_success %s  failures %d",
			stateText(*row, st),
			formatDuration(row.SampleAge),
			formatDuration(row.ScrapeDuration),
			formatClock(row.LastSuccess),
			row.ConsecutiveFailures,
		),
	}
	if row.Restarted {
		lines = append(lines, st.warn.Render("[RESTART] counter and histogram windows restarted at process boundary"))
	}
	if row.LastError != "" {
		lines = append(lines, st.bad.Render("error "+row.LastError))
	}
	if len(row.Warnings) > 0 {
		lines = append(lines, st.warn.Render("warnings "+strings.Join(row.Warnings, "; ")))
	}
	lines = append(lines, "")
	if len(row.Metrics) == 0 {
		lines = append(lines, "no metrics available")
	} else {
		lines = append(lines, detailTable(*row, m.selectedMetric, m.width, st)...)
		lines = append(lines, "partial columns are marked ~; missing values are -; selector > marks the metric; value > marks a histogram bucket bound")
	}
	scrollable := len(lines) > contentHeight(m.height)
	return title, lines, scrollable
}

func detailBodyLen(row domain.ModelSnapshot) int {
	intro := detailIntroLineCount(row) + 1
	if len(row.Metrics) == 0 {
		return intro + 1
	}
	return intro + 1 + len(row.Metrics) + 1
}

func detailIntroLineCount(row domain.ModelSnapshot) int {
	lines := 3
	if row.Restarted {
		lines++
	}
	if row.LastError != "" {
		lines++
	}
	if len(row.Warnings) > 0 {
		lines++
	}
	return lines
}

func detailTableHeaderBodyIndex(row domain.ModelSnapshot) int {
	return detailIntroLineCount(row) + 1
}

func detailMetricBodyIndex(row domain.ModelSnapshot, metric int) int {
	return detailTableHeaderBodyIndex(row) + 1 + metric
}

func clampScrollOffset(offset, bodyLen, visibleLimit int) int {
	if visibleLimit <= 0 || bodyLen <= visibleLimit {
		return 0
	}
	maxOffset := bodyLen - visibleLimit
	if offset < 0 {
		return 0
	}
	if offset > maxOffset {
		return maxOffset
	}
	return offset
}

func detailTable(row domain.ModelSnapshot, selectedMetric, width int, st styles) []string {
	partials := detailPartialColumns(row.Metrics)
	columns := []tableColumn{
		{ID: "sel", Header: " ", MinWidth: 1, PreferredWidth: 1, Priority: 0, Align: alignLeft},
		{ID: "metric", Header: "METRIC", MinWidth: 14, PreferredWidth: 34, Priority: 0, Align: alignLeft, Flexible: true},
		{ID: "now", Header: "NOW", MinWidth: 7, PreferredWidth: 10, Priority: 0, Align: alignRight},
		{ID: "one", Header: "1m", Partial: partials.OneMin, MinWidth: 7, PreferredWidth: 10, Priority: 2, DropOrder: 1, Align: alignRight},
		{ID: "five", Header: "5m", Partial: partials.Five, MinWidth: 7, PreferredWidth: 10, Priority: 3, DropOrder: 1, Align: alignRight},
		{ID: "fifteen", Header: "15m", Partial: partials.Fifteen, MinWidth: 7, PreferredWidth: 10, Priority: 4, DropOrder: 1, Align: alignRight},
		{ID: "min15", Header: "MIN 15m", Partial: partials.MinFifteen, MinWidth: 8, PreferredWidth: 10, Priority: 0, Align: alignRight},
		{ID: "max15", Header: "MAX 15m", Partial: partials.MaxFifteen, MinWidth: 8, PreferredWidth: 10, Priority: 0, Align: alignRight},
	}
	rows := make([][]tableCell, 0, len(row.Metrics))
	for i, metric := range row.Metrics {
		selector := " "
		if i == selectedMetric {
			selector = ">"
		}
		rows = append(rows, []tableCell{
			{Text: selector},
			{Text: detailMetricLabel(metric)},
			{Text: formatMetricValue(metric.Now, metric.Unit)},
			{Text: formatMetricValue(metric.OneMin, metric.Unit)},
			{Text: formatMetricValue(metric.Five, metric.Unit)},
			{Text: formatMetricValue(metric.Fifteen, metric.Unit)},
			{Text: formatMetricValue(metric.MinFifteen, metric.Unit)},
			{Text: formatMetricValue(metric.MaxFifteen, metric.Unit)},
		})
	}
	lines, ok := renderCellTableWithOptions(columns, rows, width, st, tableOptions{
		SelectedRow: selectedMetric,
	})
	if ok {
		return lines
	}
	return []string{"resize wider for metric table"}
}

type detailColumnPartials struct {
	OneMin     bool
	Five       bool
	Fifteen    bool
	MinFifteen bool
	MaxFifteen bool
}

func detailPartialColumns(rows []domain.MetricRow) detailColumnPartials {
	var partials detailColumnPartials
	for _, row := range rows {
		partials.OneMin = partials.OneMin || valuePartial(row.OneMin)
		partials.Five = partials.Five || valuePartial(row.Five)
		partials.Fifteen = partials.Fifteen || valuePartial(row.Fifteen)
		partials.MinFifteen = partials.MinFifteen || valuePartial(row.MinFifteen)
		partials.MaxFifteen = partials.MaxFifteen || valuePartial(row.MaxFifteen)
	}
	return partials
}

func valuePartial(v domain.WindowValue) bool {
	return v.Available && v.Partial
}

func outcomesTitle(m Model) string {
	row := m.selected()
	if row == nil {
		return overviewTitle(m)
	}
	window := domain.DurationLabel(m.outcomeWindow)
	if outcomeWindowPartial(*row, m.outcomeWindow) {
		window += "~"
	}
	return fmt.Sprintf("gpttop | outcomes | %s @ %s | window %s", displayModel(*row), displayEndpoint(*row), window)
}

func outcomesBody(m Model, st styles) []string {
	row := m.selected()
	if row == nil {
		return overviewBody(m, st)
	}
	window := domain.DurationLabel(m.outcomeWindow)
	if outcomeWindowPartial(*row, m.outcomeWindow) {
		window += "~"
	}
	lines := []string{renderStyled("WINDOW "+window, st.outcomeWindow, st)}
	engine := row.EngineOutcomes[m.outcomeWindow]
	http := row.HTTPOutcomes[m.outcomeWindow]
	if len(engine) == 0 && len(http) == 0 {
		lines = append(lines, "outcomes unavailable")
	}
	if len(engine) > 0 {
		lines = append(lines, renderStyled("ENGINE FINISHES", st.section, st))
		lines = append(lines, engineOutcomeTable(engine, m.width, st)...)
	}
	if len(http) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, renderStyled("HTTP", st.section, st))
		lines = append(lines, httpOutcomeTable(http, m.width, st)...)
	}
	return lines
}

func engineOutcomeTable(outcomes []domain.EngineOutcome, width int, st styles) []string {
	columns := []tableColumn{
		{ID: "reason", Header: "REASON", MinWidth: 8, PreferredWidth: 30, Priority: 0, Align: alignLeft, Flexible: true},
		{ID: "count", Header: "COUNT", MinWidth: 7, PreferredWidth: 10, Priority: 0, Align: alignRight},
	}
	rows := make([][]tableCell, 0, len(outcomes))
	for _, out := range outcomes {
		reason := out.Reason
		if reason == "" {
			reason = "reason unavailable"
		}
		rows = append(rows, []tableCell{
			{Text: reason},
			{Text: formatMetricValue(out.Count, "count")},
		})
	}
	lines, ok := renderCellTableWithOptions(columns, rows, width, st, tableOptions{
		SelectedRow: -1,
		RowStyle: func(row int) lipgloss.Style {
			return engineOutcomeStyle(outcomes[row].Reason, st)
		},
	})
	if ok {
		return lines
	}
	return []string{"resize wider for engine outcome table"}
}

func httpOutcomeTable(outcomes []domain.HTTPOutcome, width int, st styles) []string {
	columns := []tableColumn{
		{ID: "status", Header: "STATUS", MinWidth: 6, PreferredWidth: 10, Priority: 0, Align: alignLeft},
		{ID: "target", Header: "TARGET", MinWidth: 14, PreferredWidth: 48, Priority: 0, Align: alignLeft, Flexible: true},
		{ID: "count", Header: "COUNT", MinWidth: 7, PreferredWidth: 10, Priority: 0, Align: alignRight},
	}
	rows := make([][]tableCell, 0, len(outcomes))
	for _, out := range outcomes {
		status := out.Status
		if status == "" {
			status = "status unavailable"
		}
		target := strings.TrimSpace(strings.Join(nonEmpty(out.Method, out.Handler), " "))
		if target == "" {
			target = "handler unavailable"
		}
		rows = append(rows, []tableCell{
			{Text: status},
			{Text: target},
			{Text: formatMetricValue(out.Count, "count")},
		})
	}
	lines, ok := renderCellTableWithOptions(columns, rows, width, st, tableOptions{
		SelectedRow: -1,
		RowStyle: func(row int) lipgloss.Style {
			return httpOutcomeStyle(outcomes[row].Status, st)
		},
	})
	if ok {
		return lines
	}
	return []string{"resize wider for HTTP outcome table"}
}

func engineOutcomeStyle(reason string, st styles) lipgloss.Style {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "stop", "eos":
		return st.good
	case "abort", "length":
		return st.bad
	case "":
		return st.warn
	default:
		return lipgloss.Style{}
	}
}

func httpOutcomeStyle(status string, st styles) lipgloss.Style {
	switch httpStatusClass(status) {
	case 2:
		return st.good
	case 4, 5:
		return st.bad
	default:
		return lipgloss.Style{}
	}
}

func httpStatusClass(status string) int {
	status = strings.ToLower(strings.TrimSpace(status))
	if len(status) == 0 || status[0] < '1' || status[0] > '5' {
		return 0
	}
	if len(status) == 3 && status[1:] == "xx" {
		return int(status[0] - '0')
	}
	if len(status) >= 3 {
		code, err := strconv.Atoi(status[:3])
		if err == nil && code >= 100 && code <= 599 {
			return code / 100
		}
	}
	return 0
}

func renderStyled(text string, style lipgloss.Style, st styles) string {
	if st.noColor {
		return text
	}
	return style.Render(text)
}

func outcomeWindowPartial(row domain.ModelSnapshot, window time.Duration) bool {
	if window == domain.OutcomeAllTime {
		return false
	}
	if row.ObservationDuration+time.Millisecond < window {
		return true
	}
	for _, outcome := range row.EngineOutcomes[window] {
		if outcome.Count.Available && outcome.Count.Partial {
			return true
		}
	}
	for _, outcome := range row.HTTPOutcomes[window] {
		if outcome.Count.Available && outcome.Count.Partial {
			return true
		}
	}
	return false
}

func helpLines(m Model, st styles) []string {
	lines := []string{"Keys"}
	for _, binding := range keyBindings {
		if !binding.Help {
			continue
		}
		lines = append(lines, fmt.Sprintf("  %-10s %s", binding.Keys, binding.Action))
	}
	lines = append(lines, "", "Overview")
	for _, binding := range keyBindings {
		if binding.Overview {
			lines = append(lines, fmt.Sprintf("  %-10s %s", binding.Keys, binding.Action))
		}
	}
	lines = append(lines, "", "Detail")
	for _, binding := range keyBindings {
		if binding.Detail {
			lines = append(lines, fmt.Sprintf("  %-10s %s", binding.Keys, binding.Action))
		}
	}
	lines = append(lines, "", "Outcomes")
	for _, binding := range keyBindings {
		if binding.Outcomes {
			lines = append(lines, fmt.Sprintf("  %-10s %s", binding.Keys, binding.Action))
		}
	}
	return lines
}

func footerLine(m Model, detailScrollable bool) string {
	var parts []string
	for _, binding := range keyBindings {
		if binding.Scroll && !detailScrollable {
			continue
		}
		if skipFooterBinding(m, binding) {
			continue
		}
		include := false
		if m.showHelp {
			include = binding.Help
		} else {
			switch m.mode {
			case ViewDetail:
				include = binding.Detail
			case ViewOutcomes:
				include = binding.Outcomes
			default:
				include = binding.Overview
			}
		}
		if include {
			parts = append(parts, binding.Keys+" "+footerAction(binding.Action))
		}
	}
	if m.lastOp.Message != "" {
		parts = append([]string{m.lastOp.Level + ": " + m.lastOp.Message}, parts...)
	}
	return "keys: " + strings.Join(parts, "  ")
}

func skipFooterBinding(m Model, binding keyBinding) bool {
	if m.showHelp {
		return binding.Action == "help"
	}
	if m.mode == ViewDetail && binding.Keys == "esc" {
		return true
	}
	if m.mode != ViewOverview && binding.Action == "refresh" {
		return true
	}
	return false
}

func footerAction(action string) string {
	switch action {
	case "select row":
		return "row"
	case "outcome window":
		return "window"
	case "close help":
		return "close"
	default:
		return action
	}
}

func contentHeight(height int) int {
	if height <= 0 {
		return 1 << 30
	}
	if height <= 3 {
		return 0
	}
	return height - 3
}

func stateLabel(row domain.ModelSnapshot) string {
	text := "[" + string(row.State) + "]"
	if row.Unsupported && row.State != domain.StateUnsupported {
		text += "[UNSUPPORTED]"
	}
	if row.Restarted {
		text += "[RESTART]"
	}
	return text
}

func stateStyle(row domain.ModelSnapshot, st styles) lipgloss.Style {
	if st.noColor {
		return lipgloss.Style{}
	}
	switch row.State {
	case domain.StateUP:
		return st.good
	case domain.StateWarming, domain.StateStale:
		return st.warn
	case domain.StateDown, domain.StateUnsupported:
		return st.bad
	default:
		return lipgloss.Style{}
	}
}

func stateText(row domain.ModelSnapshot, st styles) string {
	text := stateLabel(row)
	if st.noColor {
		return text
	}
	return stateStyle(row, st).Render(text)
}

func displayModel(row domain.ModelSnapshot) string {
	if row.Model != "" {
		return row.Model
	}
	return "<unknown>"
}

func displayEndpoint(row domain.ModelSnapshot) string {
	if row.EndpointName != "" {
		return row.EndpointName
	}
	if row.SanitizedURL != "" {
		return row.SanitizedURL
	}
	return row.EndpointURL
}

func displayURL(row domain.ModelSnapshot) string {
	if row.SanitizedURL != "" {
		return row.SanitizedURL
	}
	if row.MetricsURL != "" {
		return row.MetricsURL
	}
	if row.EndpointURL != "" {
		return row.EndpointURL
	}
	return "-"
}

func detailMetricLabel(metric domain.MetricRow) string {
	unit := detailMetricUnit(metric.Unit)
	if unit == "" {
		return metric.Label
	}
	label := strings.TrimSpace(metric.Label)
	if label == "" {
		label = string(metric.Key)
	}
	label = strings.TrimSuffix(label, " ("+unit+")")
	return label + ", " + unit
}

func detailMetricUnit(unit string) string {
	switch strings.TrimSpace(unit) {
	case "requests/s", "req/s", "/s":
		return "req/s"
	case "tokens/s", "t/s":
		return "tok/s"
	case "requests", "count", "":
		return ""
	default:
		return unit
	}
}

func formatOverviewValue(v domain.WindowValue) string {
	return formatNumberValue(v, overviewUnit(v.Unit))
}

func overviewUnit(unit string) string {
	switch unit {
	case "requests/s", "req/s", "/s":
		return "requests/s"
	case "tokens/s", "t/s":
		return "tokens/s"
	default:
		return unit
	}
}

func formatMetricValue(v domain.WindowValue, unit string) string {
	if v.Unit != "" {
		unit = v.Unit
	}
	return formatNumberValue(v, unit)
}

func formatNumberValue(v domain.WindowValue, unit string) string {
	if !v.Available || !domain.IsFinite(v.Value) {
		return "-"
	}
	prefix := ""
	if v.MoreThan {
		prefix = ">"
	}
	suffix := ""
	switch strings.TrimSpace(unit) {
	case "%":
		return fmt.Sprintf("%s%.1f%s", prefix, v.Value, suffix)
	case "s", "sec", "seconds":
		return fmt.Sprintf("%s%.2f%s", prefix, v.Value, suffix)
	case "requests/s", "req/s", "/s", "tokens/s", "t/s":
		return fmt.Sprintf("%s%.1f%s", prefix, v.Value, suffix)
	case "cores":
		return fmt.Sprintf("%s%.2f%s", prefix, v.Value, suffix)
	case "requests", "count":
		if math.Abs(v.Value-math.Round(v.Value)) < 0.05 {
			return fmt.Sprintf("%s%.0f%s", prefix, v.Value, suffix)
		}
		return fmt.Sprintf("%s%.2f%s", prefix, v.Value, suffix)
	default:
		if math.Abs(v.Value-math.Round(v.Value)) < 0.05 {
			return fmt.Sprintf("%s%.0f%s", prefix, v.Value, suffix)
		}
		return fmt.Sprintf("%s%.2f%s", prefix, v.Value, suffix)
	}
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	if d < time.Second {
		return d.Truncate(time.Millisecond).String()
	}
	return d.Truncate(time.Second).String()
}

func formatClock(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format("15:04:05Z")
}

func fitLine(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "~"
	}
	out := truncateVisible(s, width-1) + "~"
	if strings.Contains(s, "\x1b[") {
		out += "\x1b[0m"
	}
	return out
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func truncateVisible(s string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	visible := 0
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			end := i + 2
			for end < len(s) {
				c := s[end]
				end++
				if c >= 0x40 && c <= 0x7e {
					break
				}
			}
			b.WriteString(s[i:end])
			i = end
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		rw := lipgloss.Width(string(r))
		if visible+rw > width {
			break
		}
		b.WriteRune(r)
		visible += rw
		i += size
	}
	return b.String()
}
