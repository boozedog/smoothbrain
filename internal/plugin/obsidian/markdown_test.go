package obsidian

import (
	"testing"
)

func TestParseNote_TitleFromH1(t *testing.T) {
	content := "# My Title\n\nSome body text."
	n := ParseNote("notes/test.md", content)
	if n.Title != "My Title" {
		t.Errorf("Title = %q, want %q", n.Title, "My Title")
	}
}

func TestParseNote_TitleFromFilename(t *testing.T) {
	content := "No heading here, just text."
	n := ParseNote("notes/daily-log.md", content)
	if n.Title != "daily-log" {
		t.Errorf("Title = %q, want %q", n.Title, "daily-log")
	}
}

func TestParseNote_Fields(t *testing.T) {
	content := "# Note\nStatus:: active\nPriority:: high\n"
	n := ParseNote("note.md", content)
	if n.Fields["Status"] != "active" {
		t.Errorf("Fields[Status] = %q, want %q", n.Fields["Status"], "active")
	}
	if n.Fields["Priority"] != "high" {
		t.Errorf("Fields[Priority] = %q, want %q", n.Fields["Priority"], "high")
	}
}

func TestParseNote_Sections(t *testing.T) {
	content := "# Title\n\n## Overview\nSome overview text.\n\n### Details\nDetail content here."
	n := ParseNote("note.md", content)

	// Title section (#) + Overview (##) + Details (###) = 3 sections.
	if len(n.Sections) != 3 {
		t.Fatalf("got %d sections, want 3", len(n.Sections))
	}
	if n.Sections[0].Heading != "Title" || n.Sections[0].Level != 1 {
		t.Errorf("section[0] = {%q, %d}, want {%q, %d}", n.Sections[0].Heading, n.Sections[0].Level, "Title", 1)
	}
	if n.Sections[1].Heading != "Overview" || n.Sections[1].Level != 2 {
		t.Errorf("section[1] = {%q, %d}, want {%q, %d}", n.Sections[1].Heading, n.Sections[1].Level, "Overview", 2)
	}
	if n.Sections[2].Heading != "Details" || n.Sections[2].Level != 3 {
		t.Errorf("section[2] = {%q, %d}, want {%q, %d}", n.Sections[2].Heading, n.Sections[2].Level, "Details", 3)
	}
}

func TestParseNote_PathAndRaw(t *testing.T) {
	content := "# Test\nBody."
	n := ParseNote("vault/test.md", content)
	if n.Path != "vault/test.md" {
		t.Errorf("Path = %q, want %q", n.Path, "vault/test.md")
	}
	if n.Raw != content {
		t.Errorf("Raw = %q, want %q", n.Raw, content)
	}
}

func TestParseInlineFields_Basic(t *testing.T) {
	content := "Key:: Value"
	fields := ParseInlineFields(content)
	if fields["Key"] != "Value" {
		t.Errorf("fields[Key] = %q, want %q", fields["Key"], "Value")
	}
}

func TestParseInlineFields_Multiple(t *testing.T) {
	content := "Status:: active\nType:: task\nPriority:: high"
	fields := ParseInlineFields(content)
	want := map[string]string{
		"Status":   "active",
		"Type":     "task",
		"Priority": "high",
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("fields[%s] = %q, want %q", k, fields[k], v)
		}
	}
}

func TestParseInlineFields_NoFields(t *testing.T) {
	content := "Just some regular text.\nNo inline fields here."
	fields := ParseInlineFields(content)
	if len(fields) != 0 {
		t.Errorf("got %d fields, want 0", len(fields))
	}
}

func TestParseInlineFields_Whitespace(t *testing.T) {
	content := "  Key ::  Value  "
	fields := ParseInlineFields(content)
	if fields["Key"] != "Value" {
		t.Errorf("fields[Key] = %q, want %q", fields["Key"], "Value")
	}
}

func TestParseSections_Single(t *testing.T) {
	lines := []string{"## Overview", "Some content here.", "More content."}
	sections := parseSections(lines)
	if len(sections) != 1 {
		t.Fatalf("got %d sections, want 1", len(sections))
	}
	if sections[0].Heading != "Overview" {
		t.Errorf("heading = %q, want %q", sections[0].Heading, "Overview")
	}
	if sections[0].Level != 2 {
		t.Errorf("level = %d, want 2", sections[0].Level)
	}
	if sections[0].Content != "Some content here.\nMore content." {
		t.Errorf("content = %q, want %q", sections[0].Content, "Some content here.\nMore content.")
	}
}

func TestParseSections_Multiple(t *testing.T) {
	lines := []string{
		"# Title",
		"Intro text.",
		"## Section A",
		"A content.",
		"### Subsection",
		"Sub content.",
	}
	sections := parseSections(lines)
	if len(sections) != 3 {
		t.Fatalf("got %d sections, want 3", len(sections))
	}
	if sections[0].Level != 1 || sections[1].Level != 2 || sections[2].Level != 3 {
		t.Errorf("levels = [%d, %d, %d], want [1, 2, 3]",
			sections[0].Level, sections[1].Level, sections[2].Level)
	}
}

func TestParseSections_Empty(t *testing.T) {
	lines := []string{"No headings here.", "Just plain text."}
	sections := parseSections(lines)
	if len(sections) != 0 {
		t.Errorf("got %d sections, want 0", len(sections))
	}
}

func TestParseSections_TablesExtracted(t *testing.T) {
	lines := []string{
		"## Data",
		"| Name | Value |",
		"| --- | --- |",
		"| foo | 42 |",
	}
	sections := parseSections(lines)
	if len(sections) != 1 {
		t.Fatalf("got %d sections, want 1", len(sections))
	}
	if len(sections[0].Tables) != 1 {
		t.Fatalf("got %d tables, want 1", len(sections[0].Tables))
	}
	if len(sections[0].Tables[0].Headers) != 2 {
		t.Errorf("got %d headers, want 2", len(sections[0].Tables[0].Headers))
	}
}

func TestParseTable_Basic(t *testing.T) {
	lines := []string{
		"| Name | Age |",
		"| --- | --- |",
		"| Alice | 30 |",
		"| Bob | 25 |",
	}
	tbl := ParseTable(lines)
	if len(tbl.Headers) != 2 {
		t.Fatalf("got %d headers, want 2", len(tbl.Headers))
	}
	if tbl.Headers[0] != "Name" || tbl.Headers[1] != "Age" {
		t.Errorf("headers = %v, want [Name, Age]", tbl.Headers)
	}
	if len(tbl.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(tbl.Rows))
	}
	if tbl.Rows[0][0] != "Alice" || tbl.Rows[0][1] != "30" {
		t.Errorf("row[0] = %v, want [Alice, 30]", tbl.Rows[0])
	}
	if tbl.Rows[1][0] != "Bob" || tbl.Rows[1][1] != "25" {
		t.Errorf("row[1] = %v, want [Bob, 25]", tbl.Rows[1])
	}
}

func TestParseTable_Empty(t *testing.T) {
	lines := []string{"| only one line |"}
	tbl := ParseTable(lines)
	if len(tbl.Headers) != 0 {
		t.Errorf("got %d headers, want 0", len(tbl.Headers))
	}
}

func TestParseTable_NoSeparator(t *testing.T) {
	lines := []string{
		"| Name | Age |",
		"| not a separator |",
	}
	tbl := ParseTable(lines)
	// No separator means no valid table — headers empty or rows empty.
	if len(tbl.Rows) != 0 {
		t.Errorf("got %d rows, want 0", len(tbl.Rows))
	}
}

func TestHeadingLevel_Valid(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"# Heading", 1},
		{"## Heading", 2},
		{"### Heading", 3},
		{"#### Heading", 4},
		{"##### Heading", 5},
		{"###### Heading", 6},
	}
	for _, tt := range tests {
		got := headingLevel(tt.input)
		if got != tt.want {
			t.Errorf("headingLevel(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestHeadingLevel_Invalid(t *testing.T) {
	tests := []string{
		"###nospace",
		"",
		"not a heading",
		"####### seven hashes", // >6 is invalid per markdown spec, but let's check
	}
	for _, input := range tests {
		got := headingLevel(input)
		if got != 0 {
			t.Errorf("headingLevel(%q) = %d, want 0", input, got)
		}
	}
}
