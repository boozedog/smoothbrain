package obsidian

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dmarx/smoothbrain/internal/plugin"
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

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
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

	event.Payload["summary"] = fmt.Sprintf("Added diary entry to %s", relPath)
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

	event.Payload["summary"] = fmt.Sprintf("Added link [[%s]] to %s", msg, relPath)
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
	pattern := filepath.Join(p.cfg.VaultPath, "vehicles", vehicle+".md")
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return event, fmt.Errorf("obsidian write_log: vehicle note not found: %s", vehicle)
	}

	absPath := matches[0]
	relPath, _ := filepath.Rel(p.cfg.VaultPath, absPath)

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

	event.Payload["summary"] = fmt.Sprintf("Added maintenance log entry for %s", vehicle)
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
				// Reached next section; insert before it.
				result = append(result, line)
				result = append(result, "")
				inSection = false
				inserted = true
			}
		}
		// If we're at the last line and still in section, append.
		if inSection && i == len(lines)-1 {
			result = append(result, l)
			if strings.TrimSpace(l) != "" {
				result = append(result, "")
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

// atomicWrite writes content to a file atomically via temp + rename.
func atomicWrite(path, content string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".obsidian-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}
