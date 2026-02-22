// Package claudecode provides types and streaming helpers for interacting with
// the Claude Code CLI. It handles NDJSON event parsing, token tracking, block
// extraction, and optional interactive PTY sessions.
//
// This package is intentionally placed outside internal/ so it can be imported
// by other projects and eventually extracted to a standalone module.
package claudecode

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// StreamEvent is a single NDJSON line from claude's stdout.
type StreamEvent struct {
	Type       string `json:"type"`
	Subtype    string `json:"subtype,omitempty"`
	Raw        string
	ReceivedAt time.Time
}

// TokenUsage holds token counts from the result or per-turn usage.
type TokenUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// Result is the final "result" event from claude.
type Result struct {
	Type          string     `json:"type"`
	Subtype       string     `json:"subtype"`
	ResultText    string     `json:"result"`
	IsError       bool       `json:"is_error"`
	DurationMs    int        `json:"duration_ms"`
	DurationAPIMs int        `json:"duration_api_ms"`
	NumTurns      int        `json:"num_turns"`
	CostUSD       float64    `json:"total_cost_usd"`
	SessionID     string     `json:"session_id"`
	Usage         TokenUsage `json:"usage"`
}

// Response holds everything from a single claude invocation.
type Response struct {
	Command    []string
	Prompt     string
	Events     []StreamEvent
	Result     Result
	Stderr     string
	Model      string    // extracted from assistant events
	StopReason string    // extracted from assistant events
	StartedAt  time.Time // when the command was started
}

// ContentBlock represents a single block in an assistant message (text, tool_use, or tool_result).
type ContentBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	ID      string          `json:"id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

// ToolResult represents a tool result event.
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string
	IsError   bool `json:"is_error"`
}

// TaskResultMeta holds subagent metadata from the tool_use_result JSON field.
type TaskResultMeta struct {
	AgentID           string
	TotalDurationMs   int
	TotalTokens       int
	TotalToolUseCount int
}

// BlockKind identifies the type of a ChatBlock.
type BlockKind string

const (
	BlockText       BlockKind = "text"
	BlockToolUse    BlockKind = "tool_use"
	BlockToolResult BlockKind = "tool_result"
)

// ChatBlock represents a renderable block in the chat: text, tool call, or tool result.
type ChatBlock struct {
	Kind BlockKind

	// Text fields — set when Kind=BlockText
	Text string

	// Tool fields — set when Kind=BlockToolUse or BlockToolResult
	ToolName   string
	ToolID     string
	ToolInput  string
	ToolOutput string
	IsError    bool

	// Task (subagent) fields — only set when Kind=BlockToolUse and IsTask=true
	IsTask           bool
	TaskDescription  string
	TaskSubagentType string
	TaskPrompt       string
	TaskSubBlocks    []ChatBlock
	TaskMeta         *TaskResultMeta
}

// NewTextBlock creates a text content block.
func NewTextBlock(text string) ChatBlock {
	return ChatBlock{Kind: BlockText, Text: text}
}

// NewToolUseBlock creates a tool use block.
func NewToolUseBlock(name, id, input string) ChatBlock {
	return ChatBlock{Kind: BlockToolUse, ToolName: name, ToolID: id, ToolInput: input}
}

// NewTaskBlock creates a Task (subagent) tool use block.
func NewTaskBlock(id, input string) ChatBlock {
	cb := ChatBlock{Kind: BlockToolUse, ToolName: "Task", ToolID: id, ToolInput: input, IsTask: true}
	ParseTaskInput(&cb, input)
	return cb
}

// NewToolResultBlock creates a tool result block.
func NewToolResultBlock(toolID, output string, isError bool) ChatBlock {
	return ChatBlock{Kind: BlockToolResult, ToolID: toolID, ToolOutput: output, IsError: isError}
}

// StreamDeltas holds extracted text, thinking, and tool input content from a content_block_delta event.
type StreamDeltas struct {
	Text      string
	Thinking  string
	InputJSON string
}

// ExtractDeltas extracts text, thinking, and tool input content from a content_block_delta event.
// The Claude CLI wraps streaming events as:
//
//	{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}}
//	{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"..."}}}
//	{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"..."}}}
func ExtractDeltas(raw string) StreamDeltas {
	var wrapper struct {
		Event struct {
			Type  string `json:"type"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		} `json:"event"`
	}
	if json.Unmarshal([]byte(raw), &wrapper) == nil && wrapper.Event.Type == "content_block_delta" {
		switch wrapper.Event.Delta.Type {
		case "text_delta":
			return StreamDeltas{Text: wrapper.Event.Delta.Text}
		case "thinking_delta":
			return StreamDeltas{Thinking: wrapper.Event.Delta.Thinking}
		case "input_json_delta":
			return StreamDeltas{InputJSON: wrapper.Event.Delta.PartialJSON}
		}
	}
	return StreamDeltas{}
}

// ParseEventLine parses one NDJSON line, updating result/model/stopReason as needed.
// Returns the parsed StreamEvent (nil if line is not valid JSON) and any result unmarshal error.
func ParseEventLine(line string, result *Result, model, stopReason *string) (*StreamEvent, error) {
	var ev StreamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return nil, nil //nolint:nilerr // non-JSON lines are silently skipped
	}
	ev.Raw = line
	ev.ReceivedAt = time.Now()

	var resultErr error
	switch ev.Type {
	case "result":
		if err := json.Unmarshal([]byte(line), result); err != nil {
			resultErr = err
		}
	case "assistant":
		var msg struct {
			Message struct {
				Model      string `json:"model"`
				StopReason string `json:"stop_reason"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &msg) == nil {
			if msg.Message.Model != "" {
				*model = msg.Message.Model
			}
			if msg.Message.StopReason != "" {
				*stopReason = msg.Message.StopReason
			}
		}
	}

	return &ev, resultErr
}

// PrettyJSON formats raw JSON with indentation, falling back to the raw string on error.
func PrettyJSON(raw []byte) string {
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		return pretty.String()
	}
	return string(raw)
}

// ParseTaskInput extracts Task tool input fields from JSON into the ChatBlock.
func ParseTaskInput(cb *ChatBlock, rawInput string) {
	var input struct {
		Description  string `json:"description"`
		SubagentType string `json:"subagent_type"`
		Prompt       string `json:"prompt"`
	}
	if json.Unmarshal([]byte(rawInput), &input) == nil {
		cb.TaskDescription = input.Description
		cb.TaskSubagentType = input.SubagentType
		cb.TaskPrompt = input.Prompt
	}
}

// ExtractParentToolUseID returns the parent_tool_use_id from a raw JSON event, or "".
func ExtractParentToolUseID(raw string) string {
	var ev struct {
		ParentToolUseID *string `json:"parent_tool_use_id"`
	}
	if json.Unmarshal([]byte(raw), &ev) == nil && ev.ParentToolUseID != nil && *ev.ParentToolUseID != "" {
		return *ev.ParentToolUseID
	}
	return ""
}

// FindTaskBlockIndex returns the index of the Task block with the given ToolID, or -1.
func FindTaskBlockIndex(blocks []ChatBlock, toolID string) int {
	for i := range blocks {
		if blocks[i].Kind == BlockToolUse && blocks[i].IsTask && blocks[i].ToolID == toolID {
			return i
		}
	}
	return -1
}

// ParseToolUseResult extracts TaskResultMeta from the tool_use_result JSON field of a user event.
func ParseToolUseResult(raw string) *TaskResultMeta {
	var ev struct {
		ToolUseResult *struct {
			AgentID           string `json:"agentId"`
			TotalDurationMs   int    `json:"totalDurationMs"`
			TotalTokens       int    `json:"totalTokens"`
			TotalToolUseCount int    `json:"totalToolUseCount"`
		} `json:"tool_use_result"`
	}
	if json.Unmarshal([]byte(raw), &ev) == nil && ev.ToolUseResult != nil {
		return &TaskResultMeta{
			AgentID:           ev.ToolUseResult.AgentID,
			TotalDurationMs:   ev.ToolUseResult.TotalDurationMs,
			TotalTokens:       ev.ToolUseResult.TotalTokens,
			TotalToolUseCount: ev.ToolUseResult.TotalToolUseCount,
		}
	}
	return nil
}

// ExtractToolResultContent converts tool result content (string, array, or other) to a string.
// If stripAgentID is true, text blocks prefixed with "agentId:" are excluded (used for Task results).
func ExtractToolResultContent(content any, stripAgentID bool) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var out strings.Builder
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					if stripAgentID && strings.HasPrefix(t, "agentId:") {
						continue
					}
					out.WriteString(t)
				}
			}
		}
		return out.String()
	default:
		b, _ := json.MarshalIndent(content, "", "  ")
		return string(b)
	}
}
