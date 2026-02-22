package obsidian

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boozedog/smoothbrain/internal/plugin"
	_ "modernc.org/sqlite"
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

func TestSlugify(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"basic", "Hello World", "hello-world"},
		{"special chars", "My Page!@#$% Test", "my-page-test"},
		{"multiple hyphens", "foo---bar", "foo-bar"},
		{"leading trailing", "---foo---", "foo"},
		{"empty", "", "link"},
		{"truncation", strings.Repeat("a-", 40), strings.TrimRight(strings.Repeat("a-", 30), "-")},
		{"unicode", "café résumé", "caf-r-sum"},
		{"url path", "example.com/some/page", "example-com-some-page"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slugify(tt.in)
			if got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if len(got) > 60 {
				t.Errorf("slugify(%q) length %d exceeds 60", tt.in, len(got))
			}
		})
	}
}

func TestEscapeYAML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello world", "hello world"},
		{"colon", "key: value", `"key: value"`},
		{"hash", "comment # here", `"comment # here"`},
		{"quotes", `say "hello"`, `"say \"hello\""`},
		{"brackets", "[list]", `"[list]"`},
		{"braces", "{map}", `"{map}"`},
		{"leading space", " padded", `" padded"`},
		{"trailing space", "padded ", `"padded "`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeYAML(tt.in)
			if got != tt.want {
				t.Errorf("escapeYAML(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// mockBus records emitted events for testing.
type mockBus struct {
	mu     sync.Mutex
	events []plugin.Event
}

func (b *mockBus) Emit(event plugin.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
}

func (b *mockBus) Events() []plugin.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]plugin.Event, len(b.events))
	copy(cp, b.events)
	return cp
}

func TestSaveLink_CreatesNote(t *testing.T) {
	p := newTestObsidian(t)
	bus := &mockBus{}
	p.bus = bus

	ev := plugin.Event{
		Payload: map[string]any{
			"url":          "https://example.com/article",
			"title":        "Example Article",
			"summary":      "A great article about testing.",
			"file_content": "Full article content here.",
		},
	}

	result, err := p.saveLink(context.Background(), ev, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify summary is set.
	summary, _ := result.Payload["summary"].(string)
	if !strings.Contains(summary, "Example Article") {
		t.Errorf("summary %q should contain title", summary)
	}
	if !strings.Contains(summary, "example.com") {
		t.Errorf("summary %q should contain URL", summary)
	}

	// Find the created note file.
	now := time.Now()
	yearStr := now.Format("2006")
	dateStr := now.Format("2006-01-02")
	slug := slugify("Example Article")
	notePath := filepath.Join(p.cfg.VaultPath, "links", yearStr, dateStr+"-"+slug+".md")

	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("note file not created: %v", err)
	}

	content := string(data)
	// Verify frontmatter.
	if !strings.Contains(content, "title: Example Article") {
		t.Error("note should contain title in frontmatter")
	}
	if !strings.Contains(content, "url: https://example.com/article") {
		t.Error("note should contain url in frontmatter")
	}
	if !strings.Contains(content, "saved: "+dateStr) {
		t.Error("note should contain saved date in frontmatter")
	}
	if !strings.Contains(content, "- web-clip") {
		t.Error("note should contain web-clip tag")
	}
	// Verify body sections.
	if !strings.Contains(content, "## Summary") {
		t.Error("note should contain Summary section")
	}
	if !strings.Contains(content, "A great article about testing.") {
		t.Error("note should contain summary text")
	}
	if !strings.Contains(content, "## Content") {
		t.Error("note should contain Content section")
	}
	if !strings.Contains(content, "Full article content here.") {
		t.Error("note should contain file content")
	}
	if !strings.Contains(content, "[Example Article](https://example.com/article)") {
		t.Error("note should contain source link")
	}
	// Should NOT contain tweet-specific fields.
	if strings.Contains(content, "tweet_id") {
		t.Error("note should not contain tweet_id when not a tweet")
	}
}

func TestSaveLink_TweetMetadata(t *testing.T) {
	p := newTestObsidian(t)
	bus := &mockBus{}
	p.bus = bus

	ev := plugin.Event{
		Payload: map[string]any{
			"url":             "https://x.com/user/status/123",
			"title":           "A tweet",
			"summary":         "Tweet summary",
			"tweet_id":        "123",
			"author_name":     "Test User",
			"author_username": "testuser",
		},
	}

	_, err := p.saveLink(context.Background(), ev, nil)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	slug := slugify("A tweet")
	notePath := filepath.Join(p.cfg.VaultPath, "links", now.Format("2006"), now.Format("2006-01-02")+"-"+slug+".md")

	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("note file not created: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "tweet_id: 123") {
		t.Error("note should contain tweet_id in frontmatter")
	}
	if !strings.Contains(content, `author: "@testuser"`) {
		t.Error("note should contain author in frontmatter")
	}
	// Source should use author display format.
	if !strings.Contains(content, "[Test User (@testuser)]") {
		t.Errorf("source link should use author display format, got:\n%s", content)
	}
}

func TestSaveLink_DailyNoteXref(t *testing.T) {
	p := newTestObsidian(t)
	bus := &mockBus{}
	p.bus = bus

	ev := plugin.Event{
		Payload: map[string]any{
			"url":   "https://example.com/test",
			"title": "Test Page",
		},
	}

	_, err := p.saveLink(context.Background(), ev, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Read the daily note and verify wiki-link.
	now := time.Now()
	dailyPath := filepath.Join(p.cfg.VaultPath, dailyNotePath(now))
	data, err := os.ReadFile(dailyPath)
	if err != nil {
		t.Fatalf("daily note not created: %v", err)
	}

	slug := slugify("Test Page")
	wikiLink := "[[links/" + now.Format("2006") + "/" + now.Format("2006-01-02") + "-" + slug + "]]"
	if !strings.Contains(string(data), wikiLink) {
		t.Errorf("daily note should contain wiki-link %q, got:\n%s", wikiLink, string(data))
	}
}

func TestSaveLink_EmbeddedURLReEmission(t *testing.T) {
	p := newTestObsidian(t)
	bus := &mockBus{}
	p.bus = bus

	ev := plugin.Event{
		Payload: map[string]any{
			"url":   "https://example.com/article",
			"title": "Article with links",
			"embedded_urls": []any{
				"https://other.com/page1",
				"https://another.com/page2",
				"https://third.com/page3",
			},
			"channel":     "town-square",
			"channel_id":  "ch123",
			"post_id":     "p123",
			"root_id":     "r123",
			"user_id":     "u123",
			"sender_name": "testuser",
		},
	}

	_, err := p.saveLink(context.Background(), ev, nil)
	if err != nil {
		t.Fatal(err)
	}

	events := bus.Events()
	if len(events) != 3 {
		t.Fatalf("expected 3 emitted events, got %d", len(events))
	}

	for i, e := range events {
		if e.Type != "autolink" {
			t.Errorf("event[%d] type = %q, want %q", i, e.Type, "autolink")
		}
		if e.Source != "mattermost" {
			t.Errorf("event[%d] source = %q, want %q", i, e.Source, "mattermost")
		}
		if e.Payload["channel"] != "town-square" {
			t.Errorf("event[%d] channel = %v, want %q", i, e.Payload["channel"], "town-square")
		}
		if e.Payload["sender_name"] != "testuser" {
			t.Errorf("event[%d] sender_name = %v, want %q", i, e.Payload["sender_name"], "testuser")
		}
	}

	// Verify URLs.
	if events[0].Payload["url"] != "https://other.com/page1" {
		t.Errorf("event[0] url = %v, want https://other.com/page1", events[0].Payload["url"])
	}
}

func TestSaveLink_EmbeddedURLLimit(t *testing.T) {
	p := newTestObsidian(t)
	bus := &mockBus{}
	p.bus = bus

	urls := make([]any, 10)
	for i := range urls {
		urls[i] = "https://example.com/page" + string(rune('0'+i))
	}

	ev := plugin.Event{
		Payload: map[string]any{
			"url":           "https://example.com",
			"title":         "Many links",
			"embedded_urls": urls,
		},
	}

	_, err := p.saveLink(context.Background(), ev, nil)
	if err != nil {
		t.Fatal(err)
	}

	events := bus.Events()
	if len(events) != 5 {
		t.Fatalf("expected max 5 emitted events, got %d", len(events))
	}
}

func TestSaveLink_SkipsTweetURLsInReEmission(t *testing.T) {
	p := newTestObsidian(t)
	bus := &mockBus{}
	p.bus = bus

	ev := plugin.Event{
		Payload: map[string]any{
			"url":   "https://x.com/user/status/456",
			"title": "A tweet with links",
			"embedded_urls": []any{
				"https://twitter.com/other/status/789",
				"https://x.com/someone/status/101",
				"https://example.com/safe-page",
				"https://x.com/profile",
			},
		},
	}

	_, err := p.saveLink(context.Background(), ev, nil)
	if err != nil {
		t.Fatal(err)
	}

	events := bus.Events()
	// Only non-tweet URLs should be emitted.
	// "https://x.com/profile" doesn't have /status/ so it should pass.
	if len(events) != 2 {
		t.Fatalf("expected 2 emitted events (skipping tweet URLs), got %d", len(events))
	}

	emittedURLs := make(map[string]bool)
	for _, e := range events {
		emittedURLs[e.Payload["url"].(string)] = true
	}
	if !emittedURLs["https://example.com/safe-page"] {
		t.Error("expected safe-page URL to be emitted")
	}
	if !emittedURLs["https://x.com/profile"] {
		t.Error("expected x.com/profile URL to be emitted (no /status/ path)")
	}
	if emittedURLs["https://twitter.com/other/status/789"] {
		t.Error("twitter.com status URL should not be emitted")
	}
	if emittedURLs["https://x.com/someone/status/101"] {
		t.Error("x.com status URL should not be emitted")
	}
}
