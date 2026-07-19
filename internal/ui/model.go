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

	mode          ViewMode
	selectedRow   int
	detailOffset  int
	outcomeWindow time.Duration
	groupByModel  bool
	showHelp      bool
	lastOp        OperationalMsg
	quitting      bool
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
	case tea.KeyMsg:
		return m.handleKey(v)
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
}

func (m *Model) clampSelection() {
	if len(m.snapshot.Rows) == 0 {
		m.selectedRow = 0
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
	case "5":
		m.outcomeWindow = 5 * time.Minute
	case "0":
		m.outcomeWindow = 15 * time.Minute
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

func (m *Model) moveSelection(delta int) {
	if m.mode == ViewOverview {
		m.selectedRow += delta
		return
	}
	if m.mode == ViewDetail {
		m.scrollDetail(delta)
	}
}

func (m *Model) scrollDetail(delta int) {
	if m.mode != ViewDetail {
		return
	}
	m.detailOffset += delta
	if m.detailOffset < 0 {
		m.detailOffset = 0
	}
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
