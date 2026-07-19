package ui

import (
	"os"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"gpttop/internal/domain"
)

type ViewMode int

const (
	ViewOverview ViewMode = iota
	ViewDetail
	ViewOutcomes
)

type Options struct {
	NoColor       bool
	InitialWidth  int
	InitialHeight int
	Refresh       func() tea.Cmd
}

type SnapshotMsg struct {
	Snapshot domain.AppSnapshot
}

type UpdateMsg struct {
	Snapshot domain.AppSnapshot
}

type OperationalMsg struct {
	At      time.Time
	Level   string
	Message string
}

type RefreshRequestedMsg struct {
	At time.Time
}

type Model struct {
	provider domain.SnapshotProvider
	opts     Options

	snapshot domain.AppSnapshot
	ready    bool
	width    int
	height   int

	mode           ViewMode
	selectedRow    int
	selectedMetric int
	detailOffset   int
	outcomeWindow  time.Duration
	groupByModel   bool
	showHelp       bool
	lastOp         OperationalMsg
	quitting       bool
}

func NewModel(provider domain.SnapshotProvider, opts Options) Model {
	noColor := opts.NoColor || os.Getenv("NO_COLOR") != ""
	width := opts.InitialWidth
	if width <= 0 {
		width = 100
	}
	height := opts.InitialHeight
	if height <= 0 {
		height = 30
	}
	return Model{
		provider:      provider,
		opts:          opts,
		width:         width,
		height:        height,
		outcomeWindow: time.Minute,
		groupByModel:  false,
		snapshot:      domain.AppSnapshot{NoColor: noColor},
	}
}

func NewProgram(provider domain.SnapshotProvider, opts Options, teaOptions ...tea.ProgramOption) *tea.Program {
	model := NewModel(provider, opts)
	return tea.NewProgram(model, teaOptions...)
}

func (m Model) Init() tea.Cmd {
	if m.provider == nil {
		return nil
	}
	return func() tea.Msg {
		return SnapshotMsg{Snapshot: m.provider.Snapshot(time.Now())}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case SnapshotMsg:
		m.applySnapshot(v.Snapshot)
	case UpdateMsg:
		m.applySnapshot(v.Snapshot)
	case domain.AppSnapshot:
		m.applySnapshot(v)
	case OperationalMsg:
		m.lastOp = v
	case RefreshRequestedMsg:
		m.lastOp = OperationalMsg{At: v.At, Level: "info", Message: "refresh requested"}
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
		m.ensureSelectedMetricVisible()
	case tea.KeyMsg:
		return m.handleKey(v)
	case tea.MouseMsg:
		return m.handleMouse(v)
	}
	return m, nil
}

func (m Model) View() string {
	return render(m)
}

func (m Model) Snapshot() domain.AppSnapshot {
	return m.snapshot
}

func (m Model) Mode() ViewMode {
	return m.mode
}

func (m Model) SelectedRow() int {
	return m.selectedRow
}

func (m Model) OutcomeWindow() time.Duration {
	return m.outcomeWindow
}

func (m Model) DetailOffset() int {
	return m.detailOffset
}

func (m Model) SelectedMetric() int {
	return m.selectedMetric
}

func (m Model) HelpVisible() bool {
	return m.showHelp
}

func (m Model) Quitting() bool {
	return m.quitting
}

func (m *Model) applySnapshot(s domain.AppSnapshot) {
	key := m.selectedKey()
	if m.opts.NoColor || os.Getenv("NO_COLOR") != "" {
		s.NoColor = true
	}
	m.snapshot = s
	m.ready = true
	m.clampSelection()
	m.restoreSelectedKey(key)
	m.clampSelection()
	m.ensureSelectedMetricVisible()
}

func (m *Model) clampSelection() {
	if len(m.snapshot.Rows) == 0 {
		m.selectedRow = 0
		m.selectedMetric = 0
		m.detailOffset = 0
		return
	}
	if m.selectedRow < 0 {
		m.selectedRow = 0
	}
	if m.selectedRow >= len(m.snapshot.Rows) {
		m.selectedRow = len(m.snapshot.Rows) - 1
	}
	if m.detailOffset < 0 {
		m.detailOffset = 0
	}
	m.clampMetricSelection()
}

func (m Model) selected() *domain.ModelSnapshot {
	idx, ok := m.selectedRowIndex()
	if !ok {
		return nil
	}
	return &m.snapshot.Rows[idx]
}

func (m *Model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return *m, tea.Quit
	case "?":
		m.showHelp = !m.showHelp
	case "esc":
		if m.showHelp {
			m.showHelp = false
		} else {
			m.mode = ViewOverview
		}
	case "enter":
		if len(m.snapshot.Rows) > 0 {
			if m.mode == ViewDetail {
				m.mode = ViewOverview
			} else {
				m.mode = ViewDetail
				m.detailOffset = 0
				m.clampMetricSelection()
				m.ensureSelectedMetricVisible()
			}
		}
	case "o":
		if len(m.snapshot.Rows) > 0 {
			m.mode = ViewOutcomes
		}
	case "m":
		key := m.selectedKey()
		m.groupByModel = !m.groupByModel
		m.restoreSelectedKey(key)
	case "1":
		m.outcomeWindow = time.Minute
	case "2":
		m.outcomeWindow = 5 * time.Minute
	case "3":
		m.outcomeWindow = 15 * time.Minute
	case "4":
		m.outcomeWindow = domain.OutcomeAllTime
	case "r":
		m.lastOp = OperationalMsg{At: time.Now(), Level: "info", Message: "refresh requested"}
		if m.opts.Refresh != nil {
			return *m, m.opts.Refresh()
		}
		return *m, func() tea.Msg { return RefreshRequestedMsg{At: time.Now()} }
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "pgup":
		m.scrollDetail(-10)
	case "pgdown":
		m.scrollDetail(10)
	case "home":
		m.scrollDetail(-1 << 30)
	case "end":
		m.scrollDetail(1 << 30)
	}
	m.clampSelection()
	return *m, nil
}

func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	event := tea.MouseEvent(msg)
	if m.showHelp {
		return *m, nil
	}
	switch event.Type {
	case tea.MouseLeft:
		switch m.mode {
		case ViewOverview:
			if row, ok := m.overviewRowAt(event.Y); ok {
				m.selectedRow = row
				m.clampSelection()
			}
		case ViewDetail:
			if metric, ok := m.detailMetricAt(event.Y); ok {
				m.selectedMetric = metric
				m.clampMetricSelection()
				m.ensureSelectedMetricVisible()
			}
		}
	case tea.MouseWheelUp:
		m.moveSelection(-1)
		m.clampSelection()
	case tea.MouseWheelDown:
		m.moveSelection(1)
		m.clampSelection()
	}
	return *m, nil
}

func (m *Model) moveSelection(delta int) {
	if m.mode == ViewOverview {
		m.selectedRow += delta
		return
	}
	if m.mode == ViewDetail {
		m.moveMetricSelection(delta)
	}
}

func (m *Model) scrollDetail(delta int) {
	if m.mode != ViewDetail {
		return
	}
	m.moveMetricSelection(delta)
}

func (m *Model) moveMetricSelection(delta int) {
	row := m.selected()
	if row == nil || len(row.Metrics) == 0 {
		m.selectedMetric = 0
		m.detailOffset = 0
		return
	}
	m.selectedMetric += delta
	m.clampMetricSelection()
	m.ensureSelectedMetricVisible()
}

func (m *Model) clampMetricSelection() {
	row := m.selected()
	if row == nil || len(row.Metrics) == 0 {
		m.selectedMetric = 0
		m.detailOffset = 0
		return
	}
	if m.selectedMetric < 0 {
		m.selectedMetric = 0
	}
	if m.selectedMetric >= len(row.Metrics) {
		m.selectedMetric = len(row.Metrics) - 1
	}
}

func (m *Model) ensureSelectedMetricVisible() {
	if m.mode != ViewDetail {
		return
	}
	row := m.selected()
	if row == nil || len(row.Metrics) == 0 {
		m.detailOffset = 0
		return
	}
	bodyLimit := contentHeight(m.height)
	bodyLen := detailBodyLen(*row)
	if bodyLimit <= 1 || bodyLen <= bodyLimit {
		m.detailOffset = 0
		return
	}
	visibleLimit := bodyLimit - 1
	target := detailMetricBodyIndex(*row, m.selectedMetric)
	if target < m.detailOffset {
		m.detailOffset = target
	}
	if target >= m.detailOffset+visibleLimit {
		m.detailOffset = target - visibleLimit + 1
	}
	m.detailOffset = clampScrollOffset(m.detailOffset, bodyLen, visibleLimit)
}

func (m Model) overviewRowAt(y int) (int, bool) {
	if m.mode != ViewOverview || len(m.snapshot.Rows) == 0 {
		return 0, false
	}
	bodyIndex := y - screenBodyStartY
	if bodyIndex < 1 {
		return 0, false
	}
	bodyLimit := contentHeight(m.height)
	if bodyLimit >= 0 && bodyIndex >= bodyLimit {
		return 0, false
	}
	row := bodyIndex - 1
	if row < 0 || row >= len(m.rowOrder()) {
		return 0, false
	}
	return row, true
}

func (m Model) detailMetricAt(y int) (int, bool) {
	if m.mode != ViewDetail {
		return 0, false
	}
	row := m.selected()
	if row == nil || len(row.Metrics) == 0 {
		return 0, false
	}
	bodyIndex := y - screenBodyStartY
	if bodyIndex < 0 {
		return 0, false
	}
	bodyLimit := contentHeight(m.height)
	bodyLen := detailBodyLen(*row)
	if bodyLimit >= 0 && bodyLen > bodyLimit {
		if bodyLimit <= 1 {
			return 0, false
		}
		visibleLimit := bodyLimit - 1
		if bodyIndex >= visibleLimit {
			return 0, false
		}
		bodyIndex += clampScrollOffset(m.detailOffset, bodyLen, visibleLimit)
	} else if bodyLimit >= 0 && bodyIndex >= bodyLimit {
		return 0, false
	}
	metric := bodyIndex - detailMetricBodyIndex(*row, 0)
	if metric < 0 || metric >= len(row.Metrics) {
		return 0, false
	}
	return metric, true
}

func (m Model) selectedRowIndex() (int, bool) {
	order := m.rowOrder()
	if len(order) == 0 || m.selectedRow < 0 || m.selectedRow >= len(order) {
		return 0, false
	}
	return order[m.selectedRow], true
}

func (m Model) rowOrder() []int {
	order := make([]int, len(m.snapshot.Rows))
	for i := range m.snapshot.Rows {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		left := m.snapshot.Rows[order[i]]
		right := m.snapshot.Rows[order[j]]
		if m.groupByModel {
			if left.Model == right.Model {
				return left.EndpointName < right.EndpointName
			}
			return left.Model < right.Model
		}
		if left.EndpointName == right.EndpointName {
			return left.Model < right.Model
		}
		return left.EndpointName < right.EndpointName
	})
	return order
}

func (m Model) selectedKey() string {
	row := m.selected()
	if row == nil {
		return ""
	}
	return rowKey(*row)
}

func (m *Model) restoreSelectedKey(key string) {
	if key == "" {
		return
	}
	order := m.rowOrder()
	for displayIndex, rowIndex := range order {
		if rowKey(m.snapshot.Rows[rowIndex]) == key {
			m.selectedRow = displayIndex
			return
		}
	}
}

func rowKey(row domain.ModelSnapshot) string {
	return row.EndpointName + "\x00" + row.Model + "\x00" + row.SanitizedURL + "\x00" + row.EndpointURL
}
