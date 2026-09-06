package main

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

func writeCLITable(writer io.Writer, headers []string, rows [][]string) {
	if len(headers) == 0 {
		return
	}

	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = utf8.RuneCountInString(header)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], utf8.RuneCountInString(cell))
			}
		}
	}

	fmt.Fprintln(writer, formatCLITableRow(headers, widths))
	separator := make([]string, len(widths))
	for i, width := range widths {
		separator[i] = strings.Repeat("─", width)
	}
	fmt.Fprintln(writer, formatCLITableRow(separator, widths))
	for _, row := range rows {
		fmt.Fprintln(writer, formatCLITableRow(row, widths))
	}
}

func formatCLITableRow(cells []string, widths []int) string {
	parts := make([]string, len(widths))
	for i, width := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		if i < len(widths)-1 {
			cell += strings.Repeat(" ", width-utf8.RuneCountInString(cell))
		}
		parts[i] = cell
	}
	return strings.Join(parts, "  ")
}
