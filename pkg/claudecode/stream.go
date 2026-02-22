package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Options configures a Claude CLI invocation.
type Options struct {
	Binary               string   // path to claude binary (default "claude")
	Model                string   // model to use (e.g. "opus", "sonnet")
	CWD                  string   // working directory for the command
	PermissionMode       string   // permission mode (e.g. "plan", "bypassPermissions")
	AllowedTools         []string // explicit tool allowlist
	SystemPrompt         string   // system prompt to pass
	SessionID            string   // session ID for --resume
	EnvFilter            []string // env var prefixes to exclude (default: ["CLAUDECODE="])
	DisableSlashCommands bool     // pass --disable-slash-commands
	NoChrome             bool     // pass --no-chrome
	MaxTurns             int      // pass --max-turns N
	AppendSystemPrompt   string   // pass --append-system-prompt "..."
	Tools                string   // pass --tools "Bash,Edit,Read"
}

// StreamMsg is a single message from the streaming channel.
type StreamMsg struct {
	Event    *StreamEvent
	Done     bool
	Response *Response
	Err      error
}

// shellQuoteArgs formats command args as a copy-pasteable shell command.
func shellQuoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\n\"'\\$`!#&|;(){}") {
			quoted[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(a) + `"`
		} else {
			quoted[i] = a
		}
	}
	return strings.Join(quoted, " ")
}

// BuildCmd constructs the exec.Cmd for a claude invocation.
func BuildCmd(prompt string, opts Options) *exec.Cmd {
	args := []string{"-p"}

	if opts.Tools != "" {
		args = append(args, "--tools", opts.Tools)
	}
	args = append(args, "--output-format", "stream-json", "--verbose")
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", opts.PermissionMode)
	}
	for _, tool := range opts.AllowedTools {
		args = append(args, "--allowedTools", tool)
	}
	if opts.SystemPrompt != "" {
		args = append(args, "--system-prompt", opts.SystemPrompt)
	}
	if opts.SessionID != "" {
		args = append(args, "--resume", opts.SessionID)
	}
	if opts.DisableSlashCommands {
		args = append(args, "--disable-slash-commands")
	}
	if opts.NoChrome {
		args = append(args, "--no-chrome")
	}
	if opts.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(opts.MaxTurns))
	}
	if opts.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.AppendSystemPrompt)
	}
	args = append(args, prompt)

	binary := opts.Binary
	if binary == "" {
		binary = "claude"
	}

	cmd := exec.Command(binary, args...) //nolint:gosec // binary path is from trusted config

	if opts.CWD != "" {
		cmd.Dir = opts.CWD
	}

	// Filter environment variables
	filters := opts.EnvFilter
	if len(filters) == 0 {
		filters = []string{"CLAUDECODE="}
	}
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		skip := false
		for _, f := range filters {
			if strings.HasPrefix(e, f) {
				skip = true
				break
			}
		}
		if !skip {
			filtered = append(filtered, e)
		}
	}
	cmd.Env = filtered

	return cmd
}

// Stream spawns claude in print mode and returns a channel that emits
// events incrementally. The channel is closed after the final StreamMsg{Done: true}.
func Stream(prompt string, opts Options) (<-chan StreamMsg, *exec.Cmd, error) {
	cmd := BuildCmd(prompt, opts)
	slog.Debug("claudecode: constructed command", "cmd", shellQuoteArgs(cmd.Args), "cwd", cmd.Dir)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start claude: %w", err)
	}

	// Open wire log file if enabled
	var wl *os.File
	if wireLogState.enabled.Load() {
		wireLogState.once.Do(func() {
			wireLogState.path = fmt.Sprintf("/tmp/claudecode-%s.jsonl", startedAt.Format("20060102-150405"))
		})
		var wireLogErr error
		wl, wireLogErr = os.OpenFile(wireLogState.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // wire log is a debug tool, not sensitive
		if wireLogErr != nil {
			wl = nil // non-fatal
		}
	}

	ch := make(chan StreamMsg, 64)

	go func() {
		defer close(ch)
		if wl != nil {
			defer wl.Close()
			header, _ := json.Marshal(map[string]any{
				"_wire":      "request",
				"_ts":        startedAt.Format(time.RFC3339Nano),
				"prompt":     prompt,
				"session_id": opts.SessionID,
				"command":    cmd.Args,
			})
			_, _ = fmt.Fprintf(wl, "%s\n", header)
		}

		var events []StreamEvent
		var result Result
		var model, stopReason string

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			if wl != nil {
				_, _ = fmt.Fprintf(wl, "%s\n", line) //nolint:gosec // wire log writes raw NDJSON from trusted subprocess
			}

			ev, resultErr := ParseEventLine(line, &result, &model, &stopReason)
			if ev == nil {
				continue
			}
			if resultErr != nil && wl != nil {
				errJSON, _ := json.Marshal(map[string]any{
					"_wire": "error",
					"_ts":   time.Now().Format(time.RFC3339Nano),
					"error": resultErr.Error(),
				})
				_, _ = fmt.Fprintf(wl, "%s\n", errJSON)
			}
			events = append(events, *ev)

			ch <- StreamMsg{Event: ev}
		}

		if scanErr := scanner.Err(); scanErr != nil {
			if wl != nil {
				errJSON, _ := json.Marshal(map[string]any{
					"_wire":  "error",
					"_ts":    time.Now().Format(time.RFC3339Nano),
					"error":  scanErr.Error(),
					"source": "scanner",
				})
				_, _ = fmt.Fprintf(wl, "%s\n", errJSON)
			}
		}

		waitErr := cmd.Wait()

		resp := &Response{
			Command:    cmd.Args,
			Prompt:     prompt,
			Events:     events,
			Result:     result,
			Stderr:     stderr.String(),
			Model:      model,
			StopReason: stopReason,
			StartedAt:  startedAt,
		}

		if wl != nil {
			trailer, _ := json.Marshal(map[string]any{
				"_wire":    "done",
				"_ts":      time.Now().Format(time.RFC3339Nano),
				"exit_err": fmt.Sprintf("%v", waitErr),
				"stderr":   stderr.String(),
				"model":    model,
				"stop":     stopReason,
			})
			_, _ = fmt.Fprintf(wl, "%s\n", trailer)
		}

		if waitErr != nil {
			ch <- StreamMsg{
				Done: true,
				Err:  fmt.Errorf("claude: %w\nstderr: %s", waitErr, stderr.String()),
			}
		} else {
			ch <- StreamMsg{Done: true, Response: resp}
		}
	}()

	return ch, cmd, nil
}
