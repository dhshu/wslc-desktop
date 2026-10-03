package wslc

import (
	"fmt"
	"regexp"
	"strings"
)

// columnSep matches the gap wslc's table formatter writes between two columns:
// two or more spaces, or one or more tabs. A single space is part of a cell,
// which is why "创建者 PID" and "2 hours ago" survive intact.
var columnSep = regexp.MustCompile("[ ]{2,}|\t+")

// absoluteTime matches the leading date of a real timestamp, so a relative
// "2 hours ago" can be told apart from an absolute CREATED value.
var absoluteTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

// columnSpec maps one logical field to every spelling wslc may print for it.
type columnSpec struct {
	field   string
	aliases []string
}

// columnIndex builds the normalized alias -> field lookup for one command.
func columnIndex(specs []columnSpec) map[string]string {
	m := make(map[string]string, 4*len(specs))
	for _, spec := range specs {
		for _, alias := range spec.aliases {
			key := normalizeColumn(alias)
			if key == "" || spec.field == "" {
				continue
			}
			m[key] = spec.field
		}
	}
	return m
}

// normalizeColumn canonicalizes a header cell: whitespace (including full-width
// spaces) is removed and the result is upper-cased, so "CPU %" == "cpu%" and
// "创建者 PID" == "创建者PID".
func normalizeColumn(s string) string {
	s = strings.NewReplacer("\u00a0", " ", "\u3000", " ").Replace(s)
	return strings.ToUpper(strings.Join(strings.Fields(s), ""))
}

// parseTable parses a table-format output into one map per data row.
//
// A nil map slice with a nil error means "no rows" (empty output, only a
// copyright header, or only a header line); an error means the output could not
// be recognized and quotes its first two raw lines.
func parseTable(stdout, name string, aliases map[string]string) ([]map[string]string, error) {
	raw := firstLines(stdout, 2)
	lines := tableLines(stdout)
	if len(lines) == 0 {
		return nil, nil
	}

	header, starts := splitColumnsAt(lines[0])
	if len(header) == 0 {
		return nil, unrecognizedOutput(name, raw)
	}
	fields := make([]string, len(header))
	recognized := 0
	for i, cell := range header {
		if field, ok := aliases[normalizeColumn(cell)]; ok && field != "" {
			fields[i] = field
			recognized++
		}
	}
	if recognized == 0 {
		return nil, unrecognizedOutput(name, raw)
	}

	rows := make([]map[string]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		cells, cellStarts := splitColumnsAt(line)
		if len(cells) == 0 {
			continue
		}
		aligned := alignRow(line, cells, cellStarts, starts, len(header))
		row := make(map[string]string, recognized)
		empty := true
		for i, field := range fields {
			if field == "" {
				continue
			}
			if _, dup := row[field]; !dup {
				row[field] = aligned[i]
			}
			if strings.TrimSpace(aligned[i]) != "" {
				empty = false
			}
		}
		if empty {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// alignRow lines a data row up with the header columns.
//
// The common case (as many cells as columns) is a straight left-to-right map.
// When the row has more cells than the header, everything from the start of the
// last matched cell is filled into the last column verbatim, so a status text
// containing repeated spaces is not truncated. When the row has fewer cells
// than the header, the vertical position of each cell is used to decide which
// column it belongs to, so an empty column in the middle does not shift the
// remaining values to the left.
func alignRow(line string, cells []string, cellStarts, headerStarts []int, columns int) []string {
	out := make([]string, columns)
	if columns == 0 || len(cells) == 0 {
		return out
	}
	if len(cells) >= columns {
		copy(out, cells[:columns-1])
		if start := cellStarts[columns-1]; start >= 0 && start <= len(line) {
			out[columns-1] = strings.TrimSpace(line[start:])
		} else {
			out[columns-1] = strings.TrimSpace(strings.Join(cells[columns-1:], " "))
		}
		return out
	}

	col := 0
	for i, cell := range cells {
		if idx := indexOfInt(headerStarts, cellStarts[i]); idx >= 0 && idx >= col {
			col = idx
		}
		for col < columns-1 && col+1 < len(headerStarts) && headerStarts[col+1] <= cellStarts[i] {
			col++
		}
		if col >= columns {
			out[columns-1] = strings.TrimSpace(out[columns-1] + " " + strings.Join(cells[i:], " "))
			break
		}
		out[col] = cell
		col++
	}
	return out
}

// tableLines splits stdout into candidate table lines, dropping the noise wslc
// writes around a table: copyright banners, blanks, "[wslc] ..." diagnostics
// and warning lines.
func tableLines(stdout string) []string {
	stdout = strings.TrimPrefix(stdout, "\ufeff")
	raw := strings.Split(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimPrefix(strings.TrimSuffix(line, "\r"), "\ufeff")
		if isNoiseLine(line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

// isNoiseLine reports whether a line can never be part of a table.
func isNoiseLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(trimmed, "版权所有"), strings.HasPrefix(lower, "copyright"):
		return true
	case strings.Contains(lower, "aka.ms/privacy"):
		return true
	case strings.HasPrefix(lower, "[wslc]"), strings.HasPrefix(lower, "wslc:"):
		return true
	case strings.HasPrefix(lower, "warning"), strings.HasPrefix(trimmed, "警告"):
		return true
	}
	return isSeparatorLine(trimmed)
}

// isSeparatorLine reports whether a line is only rule characters.
func isSeparatorLine(s string) bool {
	for _, r := range s {
		switch r {
		case '-', '=', '+', '_':
		default:
			return false
		}
	}
	return s != ""
}

// splitColumnsAt splits one line into cells and records the byte offset at which
// each cell starts, which is what makes empty columns recoverable.
func splitColumnsAt(line string) ([]string, []int) {
	end := len(line)
	for end > 0 && (line[end-1] == ' ' || line[end-1] == '\t') {
		end--
	}
	line = line[:end]
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) {
		return nil, nil
	}
	var cells []string
	var starts []int
	for i < len(line) {
		start := i
		loc := columnSep.FindStringIndex(line[i:])
		if loc == nil {
			cells = append(cells, strings.TrimRight(line[i:], " \t"))
			starts = append(starts, start)
			break
		}
		cells = append(cells, strings.TrimRight(line[i:i+loc[0]], " \t"))
		starts = append(starts, start)
		i += loc[1]
	}
	return cells, starts
}

// firstLines returns at most n raw lines of stdout, for diagnostics.
func firstLines(stdout string, n int) string {
	s := strings.TrimPrefix(stdout, "\ufeff")
	parts := strings.Split(s, "\n")
	if len(parts) > n {
		parts = parts[:n]
	}
	for i := range parts {
		parts[i] = strings.TrimPrefix(strings.TrimSuffix(parts[i], "\r"), "\ufeff")
	}
	return strings.Join(parts, "\n")
}

func unrecognizedOutput(name, raw string) error {
	return fmt.Errorf("wslc: unrecognized %s output (first 2 lines):\n%s", name, raw)
}

func indexOfInt(haystack []int, needle int) int {
	for i, v := range haystack {
		if v == needle {
			return i
		}
	}
	return -1
}

// isBlank reports whether stdout carries no content at all.
func isBlank(s string) bool {
	return strings.TrimSpace(strings.TrimPrefix(s, "\ufeff")) == ""
}
