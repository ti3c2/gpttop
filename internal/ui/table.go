package ui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

type cellAlign int

const (
	alignLeft cellAlign = iota
	alignRight
)

type tableColumn struct {
	ID             string
	Header         string
	Unit           string
	Partial        bool
	MinWidth       int
	PreferredWidth int
	Priority       int
	DropOrder      int
	Align          cellAlign
	Flexible       bool
}

type tableCell struct {
	Text  string
	Style lipgloss.Style
}

type plannedColumn struct {
	column tableColumn
	index  int
	width  int
}

func renderCellTable(columns []tableColumn, rows [][]tableCell, width int, st styles, selectedRow int) ([]string, bool) {
	planned, ok := planColumns(columns, width)
	if !ok {
		return nil, false
	}
	lines := make([]string, 0, len(rows)+1)
	headerCells := make([]tableCell, len(columns))
	for i, column := range columns {
		headerCells[i] = tableCell{Text: columnHeaderText(column), Style: st.header}
	}
	lines = append(lines, renderPlannedRow(planned, headerCells, width, st, false))
	for i, row := range rows {
		lines = append(lines, renderPlannedRow(planned, row, width, st, i == selectedRow))
	}
	return lines, true
}

func planColumns(columns []tableColumn, width int) ([]plannedColumn, bool) {
	if width <= 0 {
		width = 100
	}
	visible := make([]bool, len(columns))
	for i := range visible {
		visible[i] = true
	}
	for {
		planned := make([]plannedColumn, 0, len(columns))
		for i, column := range columns {
			if !visible[i] {
				continue
			}
			w := column.PreferredWidth
			min := minimumColumnWidth(column)
			if w < min {
				w = min
			}
			planned = append(planned, plannedColumn{column: column, index: i, width: w})
		}
		shrinkColumns(planned, width)
		if totalTableWidth(planned) <= width {
			return planned, true
		}
		drop := droppableColumn(columns, visible)
		if drop < 0 {
			break
		}
		visible[drop] = false
	}
	planned := make([]plannedColumn, 0, len(columns))
	for i, column := range columns {
		if column.Priority != 0 {
			continue
		}
		min := minimumColumnWidth(column)
		planned = append(planned, plannedColumn{column: column, index: i, width: min})
	}
	shrinkColumns(planned, width)
	return planned, totalTableWidth(planned) <= width
}

func minimumColumnWidth(column tableColumn) int {
	min := column.MinWidth
	if min < 1 {
		min = 1
	}
	header := columnHeaderText(column)
	if w := lipgloss.Width(header); w > min {
		min = w
	}
	return min
}

func columnHeaderText(column tableColumn) string {
	header := column.Header
	if strings.TrimSpace(column.Unit) != "" {
		header += "(" + column.Unit + ")"
	}
	if column.Partial {
		header += "~"
	}
	return header
}

func shrinkColumns(columns []plannedColumn, width int) {
	for totalTableWidth(columns) > width {
		shrank := false
		for i := range columns {
			if !columns[i].column.Flexible {
				continue
			}
			min := minimumColumnWidth(columns[i].column)
			if columns[i].width > min {
				columns[i].width--
				shrank = true
				if totalTableWidth(columns) <= width {
					return
				}
			}
		}
		if !shrank {
			return
		}
	}
}

func totalTableWidth(columns []plannedColumn) int {
	if len(columns) == 0 {
		return 0
	}
	total := len(columns) - 1
	for _, column := range columns {
		total += column.width
	}
	return total
}

func droppableColumn(columns []tableColumn, visible []bool) int {
	drop := -1
	for i, column := range columns {
		if !visible[i] || column.Priority == 0 {
			continue
		}
		if drop < 0 ||
			column.Priority > columns[drop].Priority ||
			(column.Priority == columns[drop].Priority && column.DropOrder > columns[drop].DropOrder) {
			drop = i
		}
	}
	return drop
}

func renderPlannedRow(columns []plannedColumn, cells []tableCell, width int, st styles, selected bool) string {
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		cell := tableCell{Text: "-"}
		if column.index < len(cells) {
			cell = cells[column.index]
		}
		text := fitPlainCell(cell.Text, column.width, column.column.Align)
		if !st.noColor {
			text = cell.Style.Render(text)
		}
		parts = append(parts, text)
	}
	line := strings.Join(parts, " ")
	line = padVisible(line, width)
	if selected && !st.noColor {
		line = st.selected.Render(line)
	}
	return fitLine(line, width)
}

func fitPlainCell(s string, width int, align cellAlign) string {
	if s == "" {
		s = "-"
	}
	s = truncatePlainCells(s, width)
	padding := width - lipgloss.Width(s)
	if padding <= 0 {
		return s
	}
	if align == alignRight {
		return strings.Repeat(" ", padding) + s
	}
	return s + strings.Repeat(" ", padding)
}

func padVisible(s string, width int) string {
	if width <= 0 {
		return s
	}
	padding := width - lipgloss.Width(s)
	if padding <= 0 {
		return s
	}
	return s + strings.Repeat(" ", padding)
}

func truncatePlainCells(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "~"
	}
	var b strings.Builder
	used := 0
	limit := width - 1
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 0 {
			break
		}
		w := lipgloss.Width(string(r))
		if used+w > limit {
			break
		}
		b.WriteRune(r)
		used += w
		s = s[size:]
	}
	b.WriteByte('~')
	return b.String()
}
