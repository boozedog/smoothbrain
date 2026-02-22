package claudecode

import (
	"sync"
	"sync/atomic"
)

// wireLogState holds wire logging state, safe for concurrent access.
var wireLogState struct {
	enabled atomic.Bool
	path    string
	once    sync.Once
}

// SetWireLogEnabled sets whether wire logging is active.
func SetWireLogEnabled(enabled bool) {
	wireLogState.enabled.Store(enabled)
}

// WireLogPath returns the current wire log file path (empty if logging is disabled or no session yet).
func WireLogPath() string {
	return wireLogState.path
}
