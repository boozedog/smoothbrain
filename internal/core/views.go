package core

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/dmarx/smoothbrain/internal/config"
	"github.com/dmarx/smoothbrain/internal/plugin"
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

		// Pretty-print and syntax-highlight the JSON payload.
		if raw, ok := e["payload"].(json.RawMessage); ok {
			var buf json.RawMessage
			if json.Unmarshal(raw, &buf) == nil {
				if pp, err := json.MarshalIndent(buf, "", "  "); err == nil {
					v.PrettyPayload = colorizeJSON(string(pp))
				}
			}
		}
		if v.PrettyPayload == "" {
			v.PrettyPayload = html.EscapeString(str(e["payload"]))
		}

		v.Runs = queryPipelineRuns(s, log, v.ID)
		views = append(views, v)
	}
	return views
}

func runBadgeClass(status string) string {
	switch status {
	case "completed":
		return "uk-label uk-label-primary"
	case "failed":
		return "uk-label uk-label-destructive"
	case "running":
		return "uk-label uk-label-secondary"
	default:
		return "uk-label"
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

// sourceColor returns a stable CSS hsl color for any source/plugin name.
func sourceColor(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	hue := h.Sum32() % 360
	return fmt.Sprintf("hsl(%d, 70%%, 55%%)", hue)
}

// sourceLabelStyle returns inline CSS for a colored source badge.
func sourceLabelStyle(name string) string {
	return fmt.Sprintf("background-color: %s; color: #fff;", sourceColor(name))
}

type statusInfo struct {
	Plugins []pluginStatus
	Routes  []routeStatus
}

type pluginStatus struct {
	Name    string
	Types   string
	Color   string
	Health  string
	Message string
}

type routeStatus struct {
	Name        string
	Source      string
	Event       string
	Pipeline    string
	Sink        string
	SourceColor string
}

func logLevelClass(level string) string {
	switch level {
	case "ERROR":
		return "log-error"
	case "WARN":
		return "log-warn"
	case "DEBUG":
		return "log-debug"
	default:
		return ""
	}
}

func healthBadgeClass(status string) string {
	switch status {
	case "ok":
		return "uk-label uk-label-primary"
	case "degraded":
		return "uk-label uk-label-secondary"
	case "error":
		return "uk-label uk-label-destructive"
	default:
		return "uk-label"
	}
}

// jsonTokenRe matches JSON tokens: strings, numbers, booleans, null.
var jsonTokenRe = regexp.MustCompile(`("(?:\\.|[^"\\])*")\s*:|("(?:\\.|[^"\\])*")|\b(true|false)\b|\b(null)\b|(-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)`)

// colorizeJSON takes pretty-printed JSON and returns HTML with syntax-colored spans.
func colorizeJSON(src string) string {
	var b strings.Builder
	b.Grow(len(src) * 2)
	last := 0
	for _, m := range jsonTokenRe.FindAllStringSubmatchIndex(src, -1) {
		// Write text before this match (punctuation, whitespace).
		b.WriteString(html.EscapeString(src[last:m[0]]))
		last = m[1]

		switch {
		case m[2] >= 0: // group 1: key string (before colon)
			b.WriteString(`<span class="j-key">`)
			b.WriteString(html.EscapeString(src[m[2]:m[3]]))
			b.WriteString(`</span>`)
			// Write the colon + spacing that follows the key capture
			b.WriteString(html.EscapeString(src[m[3]:m[1]]))
		case m[4] >= 0: // group 2: value string
			b.WriteString(`<span class="j-str">`)
			b.WriteString(html.EscapeString(src[m[4]:m[5]]))
			b.WriteString(`</span>`)
		case m[6] >= 0: // group 3: boolean
			b.WriteString(`<span class="j-bool">`)
			b.WriteString(src[m[6]:m[7]])
			b.WriteString(`</span>`)
		case m[8] >= 0: // group 4: null
			b.WriteString(`<span class="j-null">`)
			b.WriteString(src[m[8]:m[9]])
			b.WriteString(`</span>`)
		case m[10] >= 0: // group 5: number
			b.WriteString(`<span class="j-num">`)
			b.WriteString(src[m[10]:m[11]])
			b.WriteString(`</span>`)
		}
	}
	b.WriteString(html.EscapeString(src[last:]))
	return b.String()
}

func buildStatusInfo(ctx context.Context, reg *plugin.Registry, routes []config.RouteConfig) statusInfo {
	var info statusInfo

	healthResults := reg.CheckHealth(ctx, 5*time.Second)
	healthMap := make(map[string]plugin.HealthResult, len(healthResults))
	for _, hr := range healthResults {
		healthMap[hr.Name] = hr
	}

	for _, p := range reg.All() {
		ps := pluginStatus{
			Name:  p.Name,
			Types: strings.Join(p.Types, ", "),
			Color: sourceColor(p.Name),
		}
		if hr, ok := healthMap[p.Name]; ok {
			ps.Health = string(hr.Status.Status)
			ps.Message = hr.Status.Message
		}
		info.Plugins = append(info.Plugins, ps)
	}

	for _, r := range routes {
		var steps []string
		for _, s := range r.Pipeline {
			steps = append(steps, s.Plugin+"."+s.Action)
		}
		info.Routes = append(info.Routes, routeStatus{
			Name:        r.Name,
			Source:      r.Source,
			Event:       r.Event,
			Pipeline:    strings.Join(steps, " → "),
			Sink:        r.Sink.Plugin,
			SourceColor: sourceColor(r.Source),
		})
	}

	return info
}
