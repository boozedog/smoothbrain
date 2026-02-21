package obsidian

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDailyNotePath(t *testing.T) {
	ts := time.Date(2025, 3, 15, 10, 30, 0, 0, time.UTC)
	got := dailyNotePath(ts)
	want := "daily/2025/2025-03-15.md"
	if got != want {
		t.Errorf("dailyNotePath() = %q, want %q", got, want)
	}
}

func TestAppendToSection_Exists(t *testing.T) {
	content := "# Title\n\n## Diary\n\nOld entry\n\n## Links\n\nSome links\n"
	got := appendToSection(content, "Diary", "New entry")
	if !strings.Contains(got, "New entry") {
		t.Errorf("result does not contain new entry:\n%s", got)
	}
	// New entry should appear before ## Links
	diaryIdx := strings.Index(got, "New entry")
	linksIdx := strings.Index(got, "## Links")
	if diaryIdx > linksIdx {
		t.Errorf("new entry (pos %d) should appear before ## Links (pos %d)", diaryIdx, linksIdx)
	}
}

func TestAppendToSection_NotFound(t *testing.T) {
	content := "# Title\n\n## Existing\n\nSome content\n"
	got := appendToSection(content, "Diary", "New entry")
	if !strings.Contains(got, "## Diary") {
		t.Errorf("result should contain new section header:\n%s", got)
	}
	if !strings.Contains(got, "New entry") {
		t.Errorf("result should contain new entry:\n%s", got)
	}
}

func TestAppendToSection_BeforeNextSection(t *testing.T) {
	content := "# Title\n\n## Diary\n\nFirst entry\n\n## Notes\n\nSome notes\n"
	got := appendToSection(content, "Diary", "Second entry")
	diaryIdx := strings.Index(got, "Second entry")
	notesIdx := strings.Index(got, "## Notes")
	if diaryIdx < 0 {
		t.Fatal("result does not contain new entry")
	}
	if notesIdx < 0 {
		t.Fatal("result does not contain ## Notes")
	}
	if diaryIdx > notesIdx {
		t.Errorf("new entry (pos %d) should appear before ## Notes (pos %d)", diaryIdx, notesIdx)
	}
}

func TestAppendToSection_AtEndOfFile(t *testing.T) {
	content := "# Title\n\n## Diary\n\nExisting entry"
	got := appendToSection(content, "Diary", "Last entry")
	if !strings.Contains(got, "Last entry") {
		t.Errorf("result does not contain new entry:\n%s", got)
	}
	// New entry should be after existing entry
	existIdx := strings.Index(got, "Existing entry")
	newIdx := strings.Index(got, "Last entry")
	if newIdx < existIdx {
		t.Errorf("new entry (pos %d) should appear after existing entry (pos %d)", newIdx, existIdx)
	}
}

func TestAppendTableRow_ExistingTable(t *testing.T) {
	content := "# Vehicle\n\n## Maintenance Log\n\n| Date | Description | Miles | Cost | Location |\n| --- | --- | --- | --- | --- |\n| 2025-01-01 | Oil change | 50000 | $50 | Shop |\n\n## Notes\n"
	values := []string{"2025-03-15", "Tire rotation", "55000", "$30", "Garage"}
	got := appendTableRow(content, "Maintenance Log", values)
	if !strings.Contains(got, "Tire rotation") {
		t.Errorf("result does not contain new row:\n%s", got)
	}
	// New row should appear after existing data row
	oldRowIdx := strings.Index(got, "Oil change")
	newRowIdx := strings.Index(got, "Tire rotation")
	if newRowIdx < oldRowIdx {
		t.Errorf("new row (pos %d) should appear after existing row (pos %d)", newRowIdx, oldRowIdx)
	}
}

func TestAppendTableRow_NoTable(t *testing.T) {
	content := "# Vehicle\n\n## Maintenance Log\n\nSome text but no table.\n\n## Notes\n"
	values := []string{"2025-03-15", "Tire rotation", "55000", "$30", "Garage"}
	got := appendTableRow(content, "Maintenance Log", values)
	if !strings.Contains(got, "Tire rotation") {
		t.Errorf("result does not contain new row:\n%s", got)
	}
}

func TestIsWithinDays_Recent(t *testing.T) {
	recent := time.Now().Format("2006-01-02")
	if !isWithinDays(recent, 7) {
		t.Error("expected recent date to be within 7 days")
	}
}

func TestIsWithinDays_Future(t *testing.T) {
	future := time.Now().AddDate(0, 0, 100).Format("2006-01-02")
	if isWithinDays(future, 7) {
		t.Error("expected date 100 days in the future to NOT be within 7 days")
	}
}

func TestIsWithinDays_Invalid(t *testing.T) {
	if isWithinDays("not-a-date", 7) {
		t.Error("expected invalid date string to return false")
	}
}

func TestAtomicWrite_Creates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	if err := atomicWrite(path, "hello world"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("got %q, want %q", string(data), "hello world")
	}
}

func TestAtomicWrite_Overwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	if err := atomicWrite(path, "original"); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, "updated"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "updated" {
		t.Errorf("got %q, want %q", string(data), "updated")
	}
}
