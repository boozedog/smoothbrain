package obsidian

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/boozedog/smoothbrain/internal/plugin"
)

func dailyNotePath(t time.Time) string {
	return filepath.Join("daily", t.Format("2006"), t.Format("2006-01-02")+".md")
}

func (p *Plugin) ensureDailyNote(t time.Time) (string, error) {
	relPath := dailyNotePath(t)
	absPath := filepath.Join(p.cfg.VaultPath, relPath)

	if _, err := os.Stat(absPath); err == nil {
		return relPath, nil
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0o750); err != nil {
		return "", fmt.Errorf("obsidian: mkdir daily: %w", err)
	}

	template := fmt.Sprintf("# %s\n\n## TODO\n\n## Completed\n\n## Links\n\n## Diary\n", t.Format("2006-01-02"))
	if err := atomicWrite(absPath, template); err != nil {
		return "", fmt.Errorf("obsidian: create daily note: %w", err)
	}

	return relPath, nil
}

func (p *Plugin) writeNote(_ context.Context, event plugin.Event, _ map[string]any) (plugin.Event, error) {
	now := time.Now()
	relPath, err := p.ensureDailyNote(now)
	if err != nil {
		return event, err
	}

	msg, _ := event.Payload["message"].(string)
	if msg == "" {
		return event, fmt.Errorf("obsidian write_note: missing message")
	}

	line := fmt.Sprintf("**%s** - %s", now.Format("15:04"), msg)

	absPath := filepath.Join(p.cfg.VaultPath, relPath)
	content, err := os.ReadFile(absPath)
	if err != nil {
		return event, fmt.Errorf("obsidian write_note: %w", err)
	}

	updated := appendToSection(string(content), "Diary", line)
	if err := atomicWrite(absPath, updated); err != nil {
		return event, fmt.Errorf("obsidian write_note: %w", err)
	}

	if err := p.IndexFile(relPath); err != nil {
		p.log.Warn("re-index after write_note failed", "error", err)
	}

	event.Payload["response"] = fmt.Sprintf("Added diary entry to %s", relPath)
	return event, nil
}

func (p *Plugin) writeLink(_ context.Context, event plugin.Event, _ map[string]any) (plugin.Event, error) {
	now := time.Now()
	relPath, err := p.ensureDailyNote(now)
	if err != nil {
		return event, err
	}

	msg, _ := event.Payload["message"].(string)
	if msg == "" {
		return event, fmt.Errorf("obsidian write_link: missing message")
	}

	line := fmt.Sprintf("- [[%s]]", msg)

	absPath := filepath.Join(p.cfg.VaultPath, relPath)
	content, err := os.ReadFile(absPath)
	if err != nil {
		return event, fmt.Errorf("obsidian write_link: %w", err)
	}

	updated := appendToSection(string(content), "Links", line)
	if err := atomicWrite(absPath, updated); err != nil {
		return event, fmt.Errorf("obsidian write_link: %w", err)
	}

	if err := p.IndexFile(relPath); err != nil {
		p.log.Warn("re-index after write_link failed", "error", err)
	}

	event.Payload["response"] = fmt.Sprintf("Added link [[%s]] to %s", msg, relPath)
	return event, nil
}

func (p *Plugin) writeLog(_ context.Context, event plugin.Event, _ map[string]any) (plugin.Event, error) {
	vehicle, _ := event.Payload["vehicle"].(string)
	if vehicle == "" {
		return event, fmt.Errorf("obsidian write_log: missing vehicle")
	}

	description, _ := event.Payload["description"].(string)
	miles, _ := event.Payload["miles"].(string)
	cost, _ := event.Payload["cost"].(string)
	location, _ := event.Payload["location"].(string)

	// Find the vehicle note file.
	vehicleDir := filepath.Clean(filepath.Join(p.cfg.VaultPath, "vehicles"))
	pattern := filepath.Clean(filepath.Join(vehicleDir, vehicle+".md"))
	if !strings.HasPrefix(pattern, vehicleDir+string(filepath.Separator)) {
		return event, fmt.Errorf("obsidian write_log: vehicle escapes vault")
	}
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return event, fmt.Errorf("obsidian write_log: vehicle note not found: %s", vehicle)
	}

	absPath := matches[0]
	relPath, err := filepath.Rel(p.cfg.VaultPath, absPath)
	if err != nil {
		return event, fmt.Errorf("obsidian write_log: resolve relative path: %w", err)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return event, fmt.Errorf("obsidian write_log: %w", err)
	}

	date := time.Now().Format("2006-01-02")
	values := []string{date, description, miles, cost, location}

	updated := appendTableRow(string(content), "Maintenance Log", values)
	if err := atomicWrite(absPath, updated); err != nil {
		return event, fmt.Errorf("obsidian write_log: %w", err)
	}

	if err := p.IndexFile(relPath); err != nil {
		p.log.Warn("re-index after write_log failed", "error", err)
	}

	event.Payload["response"] = fmt.Sprintf("Added maintenance log entry for %s", vehicle)
	return event, nil
}

// appendToSection appends a line to a named section in markdown content.
func appendToSection(content, sectionName, line string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inSection := false
	inserted := false

	for i, l := range lines {
		level := headingLevel(l)
		if level > 0 {
			heading := strings.TrimSpace(strings.TrimLeft(l, "#"))
			if strings.EqualFold(heading, sectionName) {
				inSection = true
				result = append(result, l)
				continue
			}
			if inSection {
				// Trim trailing blank lines, then insert before next section.
				for len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
					result = result[:len(result)-1]
				}
				result = append(result, line)
				result = append(result, "")
				inSection = false
				inserted = true
			}
		}
		// If we're at the last line and still in section, append.
		if inSection && i == len(lines)-1 {
			result = append(result, l)
			for len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
				result = result[:len(result)-1]
			}
			result = append(result, line)
			inserted = true
			continue
		}
		result = append(result, l)
	}

	if !inserted {
		// Section not found; append at end.
		result = append(result, fmt.Sprintf("\n## %s\n", sectionName))
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// appendTableRow appends a markdown table row to a named section.
func appendTableRow(content, sectionName string, values []string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inSection := false
	inTable := false
	inserted := false

	row := "| " + strings.Join(values, " | ") + " |"

	for i, l := range lines {
		result = append(result, l)

		level := headingLevel(l)
		if level > 0 {
			heading := strings.TrimSpace(strings.TrimLeft(l, "#"))
			if strings.EqualFold(heading, sectionName) {
				inSection = true
				inTable = false
				continue
			}
			if inSection && !inserted {
				// Insert table row before next heading.
				result = insertBefore(result, len(result)-1, row)
				inserted = true
			}
			inSection = false
			inTable = false
			continue
		}

		if inSection && isTableRow(l) {
			inTable = true
		} else if inSection && inTable && !isTableRow(l) {
			// End of table; insert row here.
			result = insertBefore(result, len(result)-1, row)
			inserted = true
			inTable = false
			inSection = false
		}

		if inSection && inTable && i == len(lines)-1 {
			result = append(result, row)
			inserted = true
		}
	}

	if !inserted {
		result = append(result, row)
	}

	return strings.Join(result, "\n")
}

func insertBefore(lines []string, idx int, line string) []string {
	result := make([]string, 0, len(lines)+1)
	result = append(result, lines[:idx]...)
	result = append(result, line)
	result = append(result, lines[idx:]...)
	return result
}

func (p *Plugin) saveLink(_ context.Context, event plugin.Event, _ map[string]any) (plugin.Event, error) {
	linkURL, _ := event.Payload["url"].(string)
	if linkURL == "" {
		return event, fmt.Errorf("obsidian save_link: missing url")
	}

	response, _ := event.Payload["response"].(string)
	fileContent, _ := event.Payload["file_content"].(string)
	title, _ := event.Payload["title"].(string)
	if title == "" {
		title = linkURL
	}

	embeddedURLs, _ := event.Payload["embedded_urls"].([]any)
	authorName, _ := event.Payload["author_name"].(string)
	authorUsername, _ := event.Payload["author_username"].(string)
	tweetID, _ := event.Payload["tweet_id"].(string)

	now := time.Now()
	dateStr := now.Format("2006-01-02")
	yearStr := now.Format("2006")

	// Build slug from title or URL hostname+path.
	slugSource := title
	if slugSource == linkURL {
		if u, err := url.Parse(linkURL); err == nil {
			slugSource = u.Hostname() + u.Path
		}
	}
	slug := slugify(slugSource)

	// Create note at links/YYYY/YYYY-MM-DD-slug.md.
	noteRelDir := filepath.Join("links", yearStr)
	noteRelPath := filepath.Join(noteRelDir, dateStr+"-"+slug+".md")
	noteAbsDir := filepath.Join(p.cfg.VaultPath, noteRelDir)
	noteAbsPath := filepath.Join(p.cfg.VaultPath, noteRelPath)

	if err := os.MkdirAll(noteAbsDir, 0o750); err != nil {
		return event, fmt.Errorf("obsidian save_link: mkdir: %w", err)
	}

	// Build frontmatter.
	var fm strings.Builder
	fm.WriteString("---\n")
	fmt.Fprintf(&fm, "title: %s\n", escapeYAML(title))
	fmt.Fprintf(&fm, "url: %s\n", linkURL)
	fmt.Fprintf(&fm, "saved: %s\n", dateStr)
	fm.WriteString("tags:\n  - web-clip\n")
	if tweetID != "" {
		fmt.Fprintf(&fm, "tweet_id: %s\n", tweetID)
		if authorUsername != "" {
			fmt.Fprintf(&fm, "author: \"@%s\"\n", authorUsername)
		}
	}
	fm.WriteString("---\n")

	// Build note body.
	var body strings.Builder
	body.WriteString(fm.String())
	if response != "" {
		fmt.Fprintf(&body, "\n## Summary\n\n%s\n", response)
	}
	if fileContent != "" {
		fmt.Fprintf(&body, "\n## Content\n\n%s\n", fileContent)
	}
	displayTitle := title
	if authorName != "" && tweetID != "" {
		displayTitle = fmt.Sprintf("%s (@%s)", authorName, authorUsername)
	}
	fmt.Fprintf(&body, "\n## Source\n\n[%s](%s)\n", displayTitle, linkURL)

	if err := atomicWrite(noteAbsPath, body.String()); err != nil {
		return event, fmt.Errorf("obsidian save_link: write note: %w", err)
	}

	// Cross-reference in daily note.
	dailyRel, err := p.ensureDailyNote(now)
	if err != nil {
		p.log.Warn("obsidian save_link: ensure daily note failed", "error", err)
	} else {
		dailyAbs := filepath.Join(p.cfg.VaultPath, dailyRel)
		content, err := os.ReadFile(dailyAbs)
		if err != nil {
			p.log.Warn("obsidian save_link: read daily note failed", "error", err)
		} else {
			wikiLink := fmt.Sprintf("- [[%s]]", strings.TrimSuffix(noteRelPath, ".md"))
			updated := appendToSection(string(content), "Links", wikiLink)
			if err := atomicWrite(dailyAbs, updated); err != nil {
				p.log.Warn("obsidian save_link: update daily note failed", "error", err)
			}
		}
	}

	// Re-emit embedded URLs (up to 5, skip tweet URLs to prevent recursion).
	if len(embeddedURLs) > 0 && p.bus != nil {
		emitted := 0
		for _, raw := range embeddedURLs {
			if emitted >= 5 {
				break
			}
			u, ok := raw.(string)
			if !ok || u == "" {
				continue
			}
			if isTweetURL(u) {
				continue
			}
			payload := map[string]any{
				"message": u,
				"url":     u,
			}
			// Carry over original event context fields.
			for _, key := range []string{"channel", "channel_id", "post_id", "root_id", "user_id", "sender_name"} {
				if v, ok := event.Payload[key]; ok {
					payload[key] = v
				}
			}
			p.bus.Emit(plugin.Event{
				Source:    "mattermost",
				Type:      "autolink",
				Payload:   payload,
				Timestamp: now,
			})
			emitted++
		}
	}

	// Index the new file.
	if err := p.IndexFile(noteRelPath); err != nil {
		p.log.Warn("obsidian save_link: index failed", "error", err)
	}

	savedMsg := fmt.Sprintf("Saved link: [%s](%s) → [[%s]]", title, linkURL, strings.TrimSuffix(noteRelPath, ".md"))
	if response != "" {
		savedMsg = fmt.Sprintf("%s\n\n%s", savedMsg, response)
	}
	event.Payload["response"] = savedMsg
	return event, nil
}

// isTweetURL checks if a URL is a tweet status URL.
func isTweetURL(u string) bool {
	return (strings.Contains(u, "twitter.com/") || strings.Contains(u, "x.com/")) && strings.Contains(u, "/status/")
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts a string to a URL-friendly slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	// Truncate to 60 chars at a hyphen boundary if possible.
	if len(s) > 60 {
		s = s[:60]
		if idx := strings.LastIndex(s, "-"); idx > 0 {
			s = s[:idx]
		}
	}

	if s == "" {
		return "link"
	}
	return s
}

// escapeYAML wraps a string in double quotes if it contains special YAML characters.
func escapeYAML(s string) string {
	if strings.ContainsAny(s, `:"#[]{}`) ||
		(len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t')) {
		escaped := strings.ReplaceAll(s, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return s
}

// atomicWrite writes content to a file atomically via temp + rename.
func atomicWrite(path, content string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".obsidian-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path) //nolint:gosec // paths are constructed from validated vault-relative paths
}
