package store

import (
	"testing"
)

func TestOpen_InMemory(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if s == nil {
		t.Fatal("Open() returned nil Store")
	}
	t.Cleanup(func() { s.Close() })
}

func TestOpen_SchemaCreated(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })

	want := map[string]bool{
		"events":         false,
		"plugin_state":   false,
		"supervisor_log": false,
		"pipeline_runs":  false,
	}

	rows, err := s.DB().Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration: %v", err)
	}

	for table, found := range want {
		if !found {
			t.Errorf("table %q not created", table)
		}
	}
}

func TestOpen_WALMode(t *testing.T) {
	// Use a file-based DB since :memory: databases report "memory" as journal mode.
	path := t.TempDir() + "/test-wal.db"
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })

	var mode string
	if err := s.DB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q", mode, "wal")
	}
}

func TestClose(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestDB_ReturnsRawDB(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })

	db := s.DB()
	if db == nil {
		t.Fatal("DB() returned nil")
	}
	if err := db.Ping(); err != nil {
		t.Errorf("DB().Ping() error = %v", err)
	}
}

func TestInsertAndQueryEvents(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	_, err = s.DB().Exec(
		`INSERT INTO events (id, source, type, payload, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"evt-1", "test", "test.event", `{"key":"value"}`, "2025-01-01T00:00:00Z",
	)
	if err != nil {
		t.Fatal(err)
	}

	var id, source, typ, payload, ts string
	err = s.DB().QueryRow("SELECT id, source, type, payload, timestamp FROM events WHERE id = ?", "evt-1").
		Scan(&id, &source, &typ, &payload, &ts)
	if err != nil {
		t.Fatal(err)
	}
	if id != "evt-1" || source != "test" || typ != "test.event" {
		t.Errorf("got (%s, %s, %s), want (evt-1, test, test.event)", id, source, typ)
	}
	if payload != `{"key":"value"}` {
		t.Errorf("payload = %q, want %q", payload, `{"key":"value"}`)
	}
}

func TestInsertAndQueryPipelineRuns(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	// Insert event first for FK constraint
	_, _ = s.DB().Exec(
		`INSERT INTO events (id, source, type, payload, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"evt-1", "test", "test.event", `{}`, "2025-01-01T00:00:00Z",
	)

	_, err = s.DB().Exec(
		`INSERT INTO pipeline_runs (event_id, route, status, started_at, finished_at, duration_ms, steps)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"evt-1", "test-route", "completed", "2025-01-01T00:00:00Z", "2025-01-01T00:00:01Z", 1000, `[]`,
	)
	if err != nil {
		t.Fatal(err)
	}

	var eventID, route, status string
	var durationMs int64
	err = s.DB().QueryRow(
		"SELECT event_id, route, status, COALESCE(duration_ms, 0) FROM pipeline_runs WHERE event_id = ?", "evt-1",
	).Scan(&eventID, &route, &status, &durationMs)
	if err != nil {
		t.Fatal(err)
	}
	if eventID != "evt-1" || route != "test-route" || status != "completed" || durationMs != 1000 {
		t.Errorf("unexpected values: %s, %s, %s, %d", eventID, route, status, durationMs)
	}
}

func TestPluginState(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	_, err = s.DB().Exec(
		`INSERT INTO plugin_state (plugin, key, value) VALUES (?, ?, ?)`,
		"myplugin", "cursor", "abc123",
	)
	if err != nil {
		t.Fatal(err)
	}

	var plug, key, value string
	err = s.DB().QueryRow(
		"SELECT plugin, key, value FROM plugin_state WHERE plugin = ? AND key = ?",
		"myplugin", "cursor",
	).Scan(&plug, &key, &value)
	if err != nil {
		t.Fatal(err)
	}
	if plug != "myplugin" || key != "cursor" || value != "abc123" {
		t.Errorf("got (%s, %s, %s), want (myplugin, cursor, abc123)", plug, key, value)
	}
}

func TestOpen_InvalidPath(t *testing.T) {
	_, err := Open("/nonexistent/dir/that/does/not/exist/db.sqlite")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestSupervisorLog(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	_, err = s.DB().Exec(
		`INSERT INTO supervisor_log (task, result) VALUES (?, ?)`,
		"healthcheck", "ok",
	)
	if err != nil {
		t.Fatal(err)
	}

	var count int
	err = s.DB().QueryRow("SELECT COUNT(*) FROM supervisor_log WHERE task = ?", "healthcheck").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}
