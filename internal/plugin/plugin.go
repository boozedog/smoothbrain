package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

type Event struct {
	ID        string         `json:"id"`
	Source    string         `json:"source"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	Timestamp time.Time      `json:"timestamp"`
}

type EventBus interface {
	Emit(event Event)
}

type Plugin interface {
	Name() string
	Init(cfg json.RawMessage) error
	Start(ctx context.Context, bus EventBus) error
	Stop() error
}

type Sink interface {
	Plugin
	HandleEvent(ctx context.Context, event Event) error
}

type Transform interface {
	Plugin
	Transform(ctx context.Context, event Event, action string, params map[string]any) (Event, error)
}

// CommandInfo describes a subcommand that a source plugin can dispatch.
type CommandInfo struct {
	Name        string
	Description string
}

// CommandAware is implemented by plugins that accept a list of routable commands.
type CommandAware interface {
	SetCommands(commands []CommandInfo)
}

// WebhookRegistrar lets source plugins register HTTP handlers.
type WebhookRegistrar interface {
	RegisterWebhook(name string, handler http.HandlerFunc)
}

// StoreAware is implemented by plugins that need access to the SQLite database.
type StoreAware interface {
	SetStore(db *sql.DB)
}

// WebhookSource is implemented by plugins that provide webhook endpoints.
type WebhookSource interface {
	RegisterWebhook(reg WebhookRegistrar)
}

// WorkspaceChannelProvider is implemented by plugins that map channels
// to workspaces (e.g. auto-chat without requiring @mention).
type WorkspaceChannelProvider interface {
	WorkspaceChannels() []string
}

type Status string

const (
	StatusOK       Status = "ok"
	StatusDegraded Status = "degraded"
	StatusError    Status = "error"
)

type HealthStatus struct {
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

// HealthChecker is optionally implemented by plugins that can report their health.
type HealthChecker interface {
	HealthCheck(ctx context.Context) HealthStatus
}

// AccessDeniedError is returned by plugins when an event fails access control.
// The router logs these but does not deliver error messages back to the sink.
type AccessDeniedError struct {
	Reason string
}

func (e *AccessDeniedError) Error() string { return e.Reason }
