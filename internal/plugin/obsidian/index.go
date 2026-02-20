package obsidian

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type SearchResult struct {
	Path    string
	Title   string
	Excerpt string
	Score   float64
}

func (p *Plugin) initSchema() error {
	schema := `
CREATE TABLE IF NOT EXISTS obsidian_notes (
    path TEXT PRIMARY KEY,
    title TEXT,
    fields TEXT,
    content TEXT,
    modified_at INTEGER
);

CREATE VIRTUAL TABLE IF NOT EXISTS obsidian_fts USING fts5(
    title, fields, content,
    content=obsidian_notes,
    content_rowid=rowid,
    tokenize='porter unicode61'
);

-- Triggers to keep FTS in sync.
CREATE TRIGGER IF NOT EXISTS obsidian_notes_ai AFTER INSERT ON obsidian_notes BEGIN
    INSERT INTO obsidian_fts(rowid, title, fields, content)
    VALUES (new.rowid, new.title, new.fields, new.content);
END;

CREATE TRIGGER IF NOT EXISTS obsidian_notes_ad AFTER DELETE ON obsidian_notes BEGIN
    INSERT INTO obsidian_fts(obsidian_fts, rowid, title, fields, content)
    VALUES ('delete', old.rowid, old.title, old.fields, old.content);
END;

CREATE TRIGGER IF NOT EXISTS obsidian_notes_au AFTER UPDATE ON obsidian_notes BEGIN
    INSERT INTO obsidian_fts(obsidian_fts, rowid, title, fields, content)
    VALUES ('delete', old.rowid, old.title, old.fields, old.content);
    INSERT INTO obsidian_fts(rowid, title, fields, content)
    VALUES (new.rowid, new.title, new.fields, new.content);
END;
`
	_, err := p.db.Exec(schema)
	return err
}

func (p *Plugin) IndexFile(relPath string) error {
	absPath := filepath.Join(p.cfg.VaultPath, relPath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("obsidian: read %s: %w", relPath, err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("obsidian: stat %s: %w", relPath, err)
	}

	note := ParseNote(relPath, string(data))
	fieldsJSON, _ := json.Marshal(note.Fields)

	_, err = p.db.Exec(`
		INSERT INTO obsidian_notes (path, title, fields, content, modified_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			title = excluded.title,
			fields = excluded.fields,
			content = excluded.content,
			modified_at = excluded.modified_at`,
		relPath, note.Title, string(fieldsJSON), note.Raw, info.ModTime().Unix(),
	)
	if err != nil {
		return fmt.Errorf("obsidian: index %s: %w", relPath, err)
	}

	p.log.Debug("indexed file", "path", relPath)
	return nil
}

func (p *Plugin) IndexVault() error {
	p.log.Info("indexing vault", "path", p.cfg.VaultPath)

	// Build map of existing mtime in DB.
	rows, err := p.db.Query("SELECT path, modified_at FROM obsidian_notes")
	if err != nil {
		return fmt.Errorf("obsidian: query existing: %w", err)
	}
	defer rows.Close()

	existing := make(map[string]int64)
	for rows.Next() {
		var path string
		var mtime int64
		if err := rows.Scan(&path, &mtime); err != nil {
			return err
		}
		existing[path] = mtime
	}

	var indexed int
	err = filepath.WalkDir(p.cfg.VaultPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Skip dotfiles/directories.
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		relPath, _ := filepath.Rel(p.cfg.VaultPath, path)
		info, err := d.Info()
		if err != nil {
			return nil
		}

		// Skip unchanged files.
		if mtime, ok := existing[relPath]; ok && mtime >= info.ModTime().Unix() {
			delete(existing, relPath)
			return nil
		}
		delete(existing, relPath)

		if err := p.IndexFile(relPath); err != nil {
			p.log.Warn("index file failed", "path", relPath, "error", err)
		} else {
			indexed++
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("obsidian: walk vault: %w", err)
	}

	// Remove stale entries (files that no longer exist).
	for path := range existing {
		if _, err := p.db.Exec("DELETE FROM obsidian_notes WHERE path = ?", path); err != nil {
			p.log.Warn("remove stale entry failed", "path", path, "error", err)
		}
	}

	p.log.Info("vault indexed", "files", indexed)
	return nil
}

func (p *Plugin) Search(query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	rows, err := p.db.Query(`
		SELECT n.path, n.title,
		       snippet(obsidian_fts, 2, '**', '**', '...', 32) AS excerpt,
		       bm25(obsidian_fts, 5.0, 3.0, 1.0) AS score
		FROM obsidian_fts f
		JOIN obsidian_notes n ON f.rowid = n.rowid
		WHERE obsidian_fts MATCH ?
		ORDER BY score
		LIMIT ?`,
		query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("obsidian: search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Path, &r.Title, &r.Excerpt, &r.Score); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
