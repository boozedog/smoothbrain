package core

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/boozedog/smoothbrain/internal/config"
	"github.com/boozedog/smoothbrain/internal/plugin"
	"github.com/boozedog/smoothbrain/internal/store"
	"github.com/google/uuid"
)

type Supervisor struct {
	tasks  []config.SupervisorTask
	bus    *Bus
	store  *store.Store
	log    *slog.Logger
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewSupervisor(tasks []config.SupervisorTask, bus *Bus, store *store.Store, log *slog.Logger) *Supervisor {
	return &Supervisor{
		tasks: tasks,
		bus:   bus,
		store: store,
		log:   log,
	}
}

func (s *Supervisor) Start(ctx context.Context) {
	if s.cancel != nil {
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	for _, task := range s.tasks {
		s.wg.Add(1)
		go s.run(ctx, task)
	}
	s.log.Info("supervisor started", "tasks", len(s.tasks))
}

func (s *Supervisor) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	s.log.Info("supervisor stopped")
}

func (s *Supervisor) run(ctx context.Context, task config.SupervisorTask) {
	defer s.wg.Done()

	if strings.Contains(task.Schedule, "@") {
		s.runDaily(ctx, task)
	} else {
		s.runInterval(ctx, task)
	}
}

func (s *Supervisor) runDaily(ctx context.Context, task config.SupervisorTask) {
	hour, min, err := parseDailySchedule(task.Schedule)
	if err != nil {
		s.log.Error("invalid daily schedule", "task", task.Name, "schedule", task.Schedule, "error", err)
		return
	}

	s.log.Info("scheduled daily task", "task", task.Name, "time", fmt.Sprintf("%02d:%02d", hour, min))

	for {
		next := nextDailyRun(hour, min)
		s.log.Debug("next run", "task", task.Name, "at", next)

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			s.fire(task)
		}
	}
}

func (s *Supervisor) runInterval(ctx context.Context, task config.SupervisorTask) {
	d, err := time.ParseDuration(task.Schedule)
	if err != nil {
		s.log.Error("invalid interval schedule", "task", task.Name, "schedule", task.Schedule, "error", err)
		return
	}

	s.log.Info("scheduled interval task", "task", task.Name, "every", d)

	ticker := time.NewTicker(d)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.fire(task)
		}
	}
}

func (s *Supervisor) fire(task config.SupervisorTask) {
	s.log.Info("supervisor firing task", "task", task.Name)

	event := plugin.Event{
		ID:        uuid.New().String(),
		Source:    "supervisor",
		Type:      task.Name,
		Payload:   map[string]any{"message": task.Prompt},
		Timestamp: time.Now(),
	}
	s.bus.Emit(event)

	_, err := s.store.DB().Exec(
		`INSERT INTO supervisor_log (task, result, timestamp) VALUES (?, ?, ?)`,
		task.Name, "emitted", time.Now(),
	)
	if err != nil {
		s.log.Error("failed to log supervisor task", "task", task.Name, "error", err)
	}
}

// parseDailySchedule extracts hours and minutes from a "daily@HH:MM" string.
func parseDailySchedule(schedule string) (int, int, error) {
	parts := strings.SplitN(schedule, "@", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected daily@HH:MM, got %q", schedule)
	}
	timeParts := strings.SplitN(parts[1], ":", 2)
	if len(timeParts) != 2 {
		return 0, 0, fmt.Errorf("expected HH:MM, got %q", parts[1])
	}
	hour, err := strconv.Atoi(timeParts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid hour: %w", err)
	}
	min, err := strconv.Atoi(timeParts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid minute: %w", err)
	}
	if hour < 0 || hour > 23 || min < 0 || min > 59 {
		return 0, 0, fmt.Errorf("time out of range: %02d:%02d", hour, min)
	}
	return hour, min, nil
}

// nextDailyRun returns the next occurrence of the given hour:minute in local time.
func nextDailyRun(hour, min int) time.Time {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
