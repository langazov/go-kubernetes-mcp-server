package rpc

import (
	"strings"
)

// Table is a minimal aligned-column renderer for list-style results, echoing
// the familiar kubectl column layout.
type Table struct {
	Title   string
	Headers []string
	Rows    [][]string
}

// NewTable creates a table with the given headers.
func NewTable(headers ...string) *Table {
	return &Table{Headers: headers}
}

// SetTitle adds an optional caption line above the table.
func (t *Table) SetTitle(s string) *Table {
	t.Title = s
	return t
}

// AddRow appends a row. Rows shorter than the header count are right-padded.
func (t *Table) AddRow(values ...string) *Table {
	t.Rows = append(t.Rows, values)
	return t
}

// Render returns the table as an aligned, space-padded string.
func (t *Table) Render() string {
	var b strings.Builder
	if t.Title != "" {
		b.WriteString(t.Title)
		b.WriteByte('\n')
	}

	cols := len(t.Headers)
	widths := make([]int, cols)
	for i, h := range t.Headers {
		widths[i] = len(h)
	}
	for _, row := range t.Rows {
		for i := 0; i < cols; i++ {
			var cell string
			if i < len(row) {
				cell = row[i]
			}
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	writeRow := func(cells []string) {
		for i := 0; i < cols; i++ {
			var cell string
			if i < len(cells) {
				cell = cells[i]
			}
			if i > 0 {
				b.WriteString("   ")
			}
			b.WriteString(cell)
			b.WriteString(strings.Repeat(" ", widths[i]-len(cell)))
		}
		b.WriteByte('\n')
	}

	writeRow(t.Headers)
	for _, row := range t.Rows {
		writeRow(row)
	}
	return b.String()
}
