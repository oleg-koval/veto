package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// printCLITable renders a terminal table with widths based on its content.
// Provider and local-model values are not bounded by their column headers.
func printCLITable(headers []string, rows [][]string) {
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

	fmt.Println(formatCLITableRow(headers, widths))
	separator := make([]string, len(widths))
	for i, width := range widths {
		separator[i] = strings.Repeat("─", width)
	}
	fmt.Println(formatCLITableRow(separator, widths))
	for _, row := range rows {
		fmt.Println(formatCLITableRow(row, widths))
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
