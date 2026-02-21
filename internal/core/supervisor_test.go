package core

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/boozedog/smoothbrain/internal/config"
	"github.com/boozedog/smoothbrain/internal/plugin"
	"github.com/boozedog/smoothbrain/internal/store"
)

func TestParseDailySchedule_Valid(t *testing.T) {
	h, m, err := parseDailySchedule("daily@09:30")
	if err != nil {
		t.Fatal(err)
	}
	if h != 9 || m != 30 {
		t.Errorf("got (%d, %d), want (9, 30)", h, m)
	}
}

func TestParseDailySchedule_Midnight(t *testing.T) {
	h, m, err := parseDailySchedule("daily@00:00")
	if err != nil {
		t.Fatal(err)
	}
	if h != 0 || m != 0 {
		t.Errorf("got (%d, %d), want (0, 0)", h, m)
	}
}

func TestParseDailySchedule_EndOfDay(t *testing.T) {
	h, m, err := parseDailySchedule("daily@23:59")
	if err != nil {
		t.Fatal(err)
	}
	if h != 23 || m != 59 {
		t.Errorf("got (%d, %d), want (23, 59)", h, m)
	}
}

func TestParseDailySchedule_NoAt(t *testing.T) {
	_, _, err := parseDailySchedule("every5m")
	if err == nil {
		t.Error("expected error for schedule without @")
	}
}

func TestParseDailySchedule_BadHour(t *testing.T) {
	_, _, err := parseDailySchedule("daily@25:00")
	if err == nil {
		t.Error("expected error for hour=25")
	}
}

func TestParseDailySchedule_BadMinute(t *testing.T) {
	_, _, err := parseDailySchedule("daily@12:60")
	if err == nil {
		t.Error("expected error for minute=60")
	}
}

func TestParseDailySchedule_NotNumber(t *testing.T) {
	_, _, err := parseDailySchedule("daily@ab:cd")
	if err == nil {
		t.Error("expected error for non-numeric time")
	}
}

func TestParseDailySchedule_MissingColon(t *testing.T) {
	_, _, err := parseDailySchedule("daily@0930")
	if err == nil {
		t.Error("expected error for missing colon")
	}
}

func TestNextDailyRun_FutureToday(t *testing.T) {
	// Schedule at 23:59 — almost certainly in the future
	next := nextDailyRun(23, 59)
	now := time.Now()

	// Must be today or tomorrow depending on exact timing
	if !next.After(now) {
		t.Errorf("expected next (%v) to be after now (%v)", next, now)
	}

	// Should be on the same calendar day (unless we're running at exactly 23:59)
	if next.Day() != now.Day() && next.Sub(now) > 24*time.Hour {
		t.Errorf("expected next run today, got %v", next)
	}
}

func TestNextDailyRun_PastToday(t *testing.T) {
	// Schedule at 00:00 — almost certainly in the past
	next := nextDailyRun(0, 0)
	now := time.Now()

	if !next.After(now) {
		t.Errorf("expected next (%v) to be after now (%v)", next, now)
	}

	// Should be tomorrow
	tomorrow := now.Add(24 * time.Hour)
	if next.Day() != tomorrow.Day() {
		t.Errorf("expected next run tomorrow (%d), got day %d", tomorrow.Day(), next.Day())
	}
}

func newTestSupervisor(t *testing.T, tasks []config.SupervisorTask) (*Supervisor, *Bus, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := NewBus(st, log)
	return NewSupervisor(tasks, bus, st, log), bus, st
}

func TestNewSupervisor(t *testing.T) {
	tasks := []config.SupervisorTask{{Name: "test", Schedule: "1h", Prompt: "hello"}}
	sup, _, _ := newTestSupervisor(t, tasks)

	if sup.bus == nil {
		t.Error("bus is nil")
	}
	if sup.store == nil {
		t.Error("store is nil")
	}
	if sup.log == nil {
		t.Error("log is nil")
	}
	if len(sup.tasks) != 1 {
		t.Errorf("tasks len = %d, want 1", len(sup.tasks))
	}
}

func TestSupervisor_Fire(t *testing.T) {
	task := config.SupervisorTask{Name: "daily-summary", Schedule: "1h", Prompt: "summarize today"}
	sup, bus, st := newTestSupervisor(t, []config.SupervisorTask{task})

	var got plugin.Event
	bus.Subscribe(func(e plugin.Event) { got = e })

	sup.fire(task)

	if got.Source != "supervisor" {
		t.Errorf("event source = %q, want %q", got.Source, "supervisor")
	}
	if got.Type != "daily-summary" {
		t.Errorf("event type = %q, want %q", got.Type, "daily-summary")
	}
	if msg, ok := got.Payload["message"]; !ok || msg != "summarize today" {
		t.Errorf("event payload message = %v, want %q", msg, "summarize today")
	}
	if got.ID == "" {
		t.Error("event ID is empty")
	}

	// Verify supervisor_log row was inserted
	var count int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM supervisor_log WHERE task = ?", "daily-summary").Scan(&count); err != nil {
		t.Fatalf("query supervisor_log: %v", err)
	}
	if count != 1 {
		t.Errorf("supervisor_log count = %d, want 1", count)
	}
}

func TestSupervisor_StartStop(t *testing.T) {
	tasks := []config.SupervisorTask{{Name: "noop", Schedule: "1h", Prompt: "noop"}}
	sup, _, _ := newTestSupervisor(t, tasks)

	ctx := context.Background()
	sup.Start(ctx)
	sup.Stop()
	// If we get here, no panic and wg completed
}

func TestSupervisor_StartIdempotent(t *testing.T) {
	tasks := []config.SupervisorTask{{Name: "noop", Schedule: "1h", Prompt: "noop"}}
	sup, _, _ := newTestSupervisor(t, tasks)

	ctx := context.Background()
	sup.Start(ctx)
	sup.Start(ctx) // second call should be a no-op
	sup.Stop()
}

func TestSupervisor_StopNilCancel(t *testing.T) {
	tasks := []config.SupervisorTask{{Name: "noop", Schedule: "1h", Prompt: "noop"}}
	sup, _, _ := newTestSupervisor(t, tasks)

	// Stop without Start — should not panic
	sup.Stop()
}
