package obsidian

import (
	"path/filepath"
	"regexp"
	"strings"
)

type NoteFile struct {
	Path     string
	Title    string
	Fields   map[string]string
	Sections []Section
	Raw      string
}

type Section struct {
	Heading string
	Level   int
	Content string
	Tables  []Table
}

type Table struct {
	Headers []string
	Rows    [][]string
}

var inlineFieldRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9 _-]*)::(.+)$`)

func ParseNote(path, content string) NoteFile {
	n := NoteFile{
		Path:   path,
		Fields: make(map[string]string),
		Raw:    content,
	}

	lines := strings.Split(content, "\n")

	// Find title: first H1 or fallback to filename.
	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			n.Title = strings.TrimSpace(line[2:])
			break
		}
	}
	if n.Title == "" {
		base := filepath.Base(path)
		n.Title = strings.TrimSuffix(base, filepath.Ext(base))
	}

	// Parse inline fields from entire content.
	n.Fields = ParseInlineFields(content)

	// Parse sections.
	n.Sections = parseSections(lines)

	return n
}

func ParseInlineFields(content string) map[string]string {
	fields := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		if m := inlineFieldRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			fields[strings.TrimSpace(m[1])] = strings.TrimSpace(m[2])
		}
	}
	return fields
}

func parseSections(lines []string) []Section {
	var sections []Section
	var current *Section
	var contentLines []string

	flushContent := func() {
		if current != nil {
			raw := strings.Join(contentLines, "\n")
			current.Content = strings.TrimSpace(raw)
			current.Tables = extractTables(contentLines)
			sections = append(sections, *current)
		}
	}

	for _, line := range lines {
		level := headingLevel(line)
		if level > 0 {
			flushContent()
			heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
			current = &Section{Heading: heading, Level: level}
			contentLines = nil
			continue
		}
		if current != nil {
			contentLines = append(contentLines, line)
		}
	}
	flushContent()

	return sections
}

func headingLevel(line string) int {
	trimmed := strings.TrimLeft(line, "#")
	level := len(line) - len(trimmed)
	if level > 0 && level <= 6 && len(trimmed) > 0 && trimmed[0] == ' ' {
		return level
	}
	return 0
}

func extractTables(lines []string) []Table {
	var tables []Table
	i := 0
	for i < len(lines) {
		if isTableRow(lines[i]) && i+1 < len(lines) && isTableSeparator(lines[i+1]) {
			t := ParseTable(lines[i:])
			if len(t.Headers) > 0 {
				tables = append(tables, t)
			}
			// Skip past the table.
			for i < len(lines) && isTableRow(lines[i]) {
				i++
			}
			continue
		}
		i++
	}
	return tables
}

func ParseTable(lines []string) Table {
	var t Table
	if len(lines) < 2 {
		return t
	}

	// Header row.
	t.Headers = parseTableCells(lines[0])
	if len(t.Headers) == 0 {
		return t
	}

	// Skip separator row.
	if !isTableSeparator(lines[1]) {
		return t
	}

	// Data rows.
	for _, line := range lines[2:] {
		if !isTableRow(line) {
			break
		}
		cells := parseTableCells(line)
		t.Rows = append(t.Rows, cells)
	}

	return t
}

func parseTableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

func isTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")
}

func isTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") {
		return false
	}
	trimmed = strings.Trim(trimmed, "|")
	for _, cell := range strings.Split(trimmed, "|") {
		cell = strings.TrimSpace(cell)
		cleaned := strings.Trim(cell, "-:")
		if cleaned != "" {
			return false
		}
	}
	return true
}

func (n NoteFile) FindSection(heading string) (Section, bool) {
	lower := strings.ToLower(heading)
	for _, s := range n.Sections {
		if strings.ToLower(s.Heading) == lower {
			return s, true
		}
	}
	return Section{}, false
}
