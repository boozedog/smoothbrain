package core

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/dmarx/smoothbrain/internal/store"
)

type eventView struct {
	ID            string
	Source        string
	Type          string
	Timestamp     string
	Route         string
	PrettyPayload string
	Runs          []pipelineRun
}

func toEventViews(events []map[string]any, s *store.Store, log *slog.Logger) []eventView {
	views := make([]eventView, 0, len(events))
	for _, e := range events {
		v := eventView{
			ID:        str(e["id"]),
			Source:    str(e["source"]),
			Type:      str(e["type"]),
			Timestamp: str(e["timestamp"]),
			Route:     str(e["route"]),
		}

		// Pretty-print the JSON payload.
		if raw, ok := e["payload"].(json.RawMessage); ok {
			var buf json.RawMessage
			if json.Unmarshal(raw, &buf) == nil {
				if pp, err := json.MarshalIndent(buf, "", "  "); err == nil {
					v.PrettyPayload = string(pp)
				}
			}
		}
		if v.PrettyPayload == "" {
			v.PrettyPayload = str(e["payload"])
		}

		v.Runs = queryPipelineRuns(s, log, v.ID)
		views = append(views, v)
	}
	return views
}

func runBadgeClass(status string) string {
	switch status {
	case "completed":
		return "badge badge-run-completed"
	case "failed":
		return "badge badge-run-failed"
	case "running":
		return "badge badge-run-running"
	default:
		return "badge"
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func durationStr(ms int64) string {
	return fmt.Sprintf("%dms", ms)
}

func parseSteps(stepsJSON string) []stepResult {
	var steps []stepResult
	if json.Unmarshal([]byte(stepsJSON), &steps) != nil {
		return nil
	}
	return steps
}
