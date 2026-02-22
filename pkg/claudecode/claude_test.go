package claudecode

import (
	"testing"
)

func TestExtractDeltas(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want StreamDeltas
	}{
		{
			name: "text_delta",
			raw:  `{"event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello world"}}}`,
			want: StreamDeltas{Text: "Hello world"},
		},
		{
			name: "thinking_delta",
			raw:  `{"event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"Let me think..."}}}`,
			want: StreamDeltas{Thinking: "Let me think..."},
		},
		{
			name: "input_json_delta",
			raw:  `{"event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"key\":"}}}`,
			want: StreamDeltas{InputJSON: `{"key":`},
		},
		{
			name: "invalid JSON",
			raw:  `not valid json at all`,
			want: StreamDeltas{},
		},
		{
			name: "non-delta event type",
			raw:  `{"event":{"type":"content_block_start","content_block":{"type":"text"}}}`,
			want: StreamDeltas{},
		},
		{
			name: "empty string",
			raw:  "",
			want: StreamDeltas{},
		},
		{
			name: "delta with unknown type",
			raw:  `{"event":{"type":"content_block_delta","delta":{"type":"unknown_delta","text":"hello"}}}`,
			want: StreamDeltas{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractDeltas(tt.raw)
			if got != tt.want {
				t.Errorf("ExtractDeltas() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExtractParentToolUseID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "with parent_tool_use_id",
			raw:  `{"type":"assistant","parent_tool_use_id":"toolu_abc123"}`,
			want: "toolu_abc123",
		},
		{
			name: "missing field",
			raw:  `{"type":"assistant"}`,
			want: "",
		},
		{
			name: "null value",
			raw:  `{"type":"assistant","parent_tool_use_id":null}`,
			want: "",
		},
		{
			name: "empty string value",
			raw:  `{"type":"assistant","parent_tool_use_id":""}`,
			want: "",
		},
		{
			name: "invalid JSON",
			raw:  `not json`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractParentToolUseID(tt.raw)
			if got != tt.want {
				t.Errorf("ExtractParentToolUseID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTaskInput(t *testing.T) {
	tests := []struct {
		name            string
		rawInput        string
		wantDescription string
		wantSubagent    string
		wantPrompt      string
	}{
		{
			name:            "valid input with all fields",
			rawInput:        `{"description":"Find bugs","subagent_type":"Bash","prompt":"Run tests"}`,
			wantDescription: "Find bugs",
			wantSubagent:    "Bash",
			wantPrompt:      "Run tests",
		},
		{
			name:            "missing optional fields",
			rawInput:        `{"description":"Search code"}`,
			wantDescription: "Search code",
		},
		{
			name:     "invalid JSON",
			rawInput: `not json`,
		},
		{
			name:     "empty JSON object",
			rawInput: `{}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := &ChatBlock{}
			ParseTaskInput(cb, tt.rawInput)
			if cb.TaskDescription != tt.wantDescription {
				t.Errorf("TaskDescription = %q, want %q", cb.TaskDescription, tt.wantDescription)
			}
			if cb.TaskSubagentType != tt.wantSubagent {
				t.Errorf("TaskSubagentType = %q, want %q", cb.TaskSubagentType, tt.wantSubagent)
			}
			if cb.TaskPrompt != tt.wantPrompt {
				t.Errorf("TaskPrompt = %q, want %q", cb.TaskPrompt, tt.wantPrompt)
			}
		})
	}
}

func TestFindTaskBlockIndex(t *testing.T) {
	blocks := []ChatBlock{
		{Kind: BlockText, Text: "Hello"},
		{Kind: BlockToolUse, ToolName: "Bash", ToolID: "tool1"},
		{Kind: BlockToolUse, ToolName: "Task", ToolID: "task1", IsTask: true},
		{Kind: BlockToolResult, ToolID: "tool1"},
		{Kind: BlockToolUse, ToolName: "Task", ToolID: "task2", IsTask: true},
	}

	tests := []struct {
		name   string
		blocks []ChatBlock
		toolID string
		want   int
	}{
		{"found first task", blocks, "task1", 2},
		{"found second task", blocks, "task2", 4},
		{"not found", blocks, "nonexistent", -1},
		{"non-Task tool_use", blocks, "tool1", -1},
		{"empty blocks", nil, "task1", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindTaskBlockIndex(tt.blocks, tt.toolID)
			if got != tt.want {
				t.Errorf("FindTaskBlockIndex() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseToolUseResult(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want *TaskResultMeta
	}{
		{
			name: "valid result",
			raw:  `{"tool_use_result":{"agentId":"agent-xyz","totalDurationMs":5000,"totalTokens":1234,"totalToolUseCount":7}}`,
			want: &TaskResultMeta{AgentID: "agent-xyz", TotalDurationMs: 5000, TotalTokens: 1234, TotalToolUseCount: 7},
		},
		{name: "null", raw: `{"tool_use_result":null}`},
		{name: "missing field", raw: `{"type":"user"}`},
		{name: "invalid JSON", raw: `not json`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseToolUseResult(tt.raw)
			if tt.want == nil {
				if got != nil {
					t.Errorf("got %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("got nil, want %+v", tt.want)
			}
			if *got != *tt.want {
				t.Errorf("got %+v, want %+v", *got, *tt.want)
			}
		})
	}
}

func TestExtractToolResultContent(t *testing.T) {
	tests := []struct {
		name         string
		content      any
		stripAgentID bool
		want         string
	}{
		{"string", "hello world", false, "hello world"},
		{
			"array of text blocks",
			[]any{
				map[string]any{"type": "text", "text": "first"},
				map[string]any{"type": "text", "text": " second"},
			},
			false, "first second",
		},
		{
			"strip agentId",
			[]any{
				map[string]any{"type": "text", "text": "agentId:abc-123"},
				map[string]any{"type": "text", "text": "actual content"},
			},
			true, "actual content",
		},
		{
			"keep agentId when not stripping",
			[]any{
				map[string]any{"type": "text", "text": "agentId:abc-123"},
				map[string]any{"type": "text", "text": "actual content"},
			},
			false, "agentId:abc-123actual content",
		},
		{"empty array", []any{}, false, ""},
		{"unknown type", 42, false, "42"},
		{"nil", nil, false, "null"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractToolResultContent(tt.content, tt.stripAgentID)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseEventLine(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantType   string
		wantNil    bool
		wantModel  string
		wantStop   string
		wantResult bool
		wantErr    bool
	}{
		{
			name:       "result event",
			line:       `{"type":"result","subtype":"success","result":"Done","duration_ms":1234,"total_cost_usd":0.05,"session_id":"sess-1","usage":{"input_tokens":100,"output_tokens":50}}`,
			wantType:   "result",
			wantResult: true,
		},
		{
			name:      "assistant event with model",
			line:      `{"type":"assistant","message":{"model":"claude-sonnet-4-20250514","stop_reason":"end_turn","content":[]}}`,
			wantType:  "assistant",
			wantModel: "claude-sonnet-4-20250514",
			wantStop:  "end_turn",
		},
		{
			name:     "content_block_start",
			line:     `{"type":"content_block_start","content_block":{"type":"text","text":""}}`,
			wantType: "content_block_start",
		},
		{
			name:    "invalid JSON",
			line:    `not json at all`,
			wantNil: true,
		},
		{
			name:    "empty string",
			line:    ``,
			wantNil: true,
		},
		{
			name:     "result with bad JSON body",
			line:     `{"type":"result","usage":"bad"}`,
			wantType: "result",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result Result
			var model, stopReason string
			ev, err := ParseEventLine(tt.line, &result, &model, &stopReason)

			if tt.wantNil {
				if ev != nil {
					t.Fatalf("got non-nil event, want nil")
				}
				return
			}
			if ev == nil {
				t.Fatalf("got nil, want event type %q", tt.wantType)
			}
			if ev.Type != tt.wantType {
				t.Errorf("type = %q, want %q", ev.Type, tt.wantType)
			}
			if ev.Raw != tt.line {
				t.Error("Raw not preserved")
			}
			if tt.wantModel != "" && model != tt.wantModel {
				t.Errorf("model = %q, want %q", model, tt.wantModel)
			}
			if tt.wantStop != "" && stopReason != tt.wantStop {
				t.Errorf("stopReason = %q, want %q", stopReason, tt.wantStop)
			}
			if tt.wantResult && result.Type != "result" {
				t.Errorf("result.Type = %q, want 'result'", result.Type)
			}
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestExtractBlocks(t *testing.T) {
	t.Run("text and tool_use from assistant", func(t *testing.T) {
		resp := &Response{
			Events: []StreamEvent{
				{Type: "assistant", Raw: `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"},{"type":"tool_use","id":"tool1","name":"Bash","input":{"command":"ls"}}]}}`},
			},
		}
		blocks := resp.ExtractBlocks()
		if len(blocks) != 2 {
			t.Fatalf("got %d blocks, want 2", len(blocks))
		}
		if blocks[0].Kind != BlockText || blocks[0].Text != "Hello" {
			t.Errorf("block[0] = %+v, want text 'Hello'", blocks[0])
		}
		if blocks[1].Kind != BlockToolUse || blocks[1].ToolName != "Bash" {
			t.Errorf("block[1] = %+v, want tool_use Bash", blocks[1])
		}
	})

	t.Run("tool_result from user event", func(t *testing.T) {
		resp := &Response{
			Events: []StreamEvent{
				{Type: "assistant", Raw: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool1","name":"Bash","input":{}}]}}`},
				{Type: "user", Raw: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool1","content":"output here"}]}}`},
			},
		}
		blocks := resp.ExtractBlocks()
		if len(blocks) != 2 {
			t.Fatalf("got %d blocks, want 2", len(blocks))
		}
		if blocks[1].Kind != BlockToolResult || blocks[1].ToolOutput != "output here" {
			t.Errorf("block[1] = %+v, want tool_result", blocks[1])
		}
	})

	t.Run("Task with subagent routing", func(t *testing.T) {
		resp := &Response{
			Events: []StreamEvent{
				{Type: "assistant", Raw: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"task1","name":"Task","input":{"description":"test","subagent_type":"Bash","prompt":"do stuff"}}]}}`},
				{Type: "assistant", Raw: `{"type":"assistant","parent_tool_use_id":"task1","message":{"content":[{"type":"tool_use","id":"sub1","name":"Read","input":{"file_path":"/tmp/test"}}]}}`},
				{Type: "user", Raw: `{"type":"user","parent_tool_use_id":"task1","message":{"content":[{"type":"tool_result","tool_use_id":"sub1","content":"file contents"}]}}`},
				{Type: "user", Raw: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"task1","content":[{"type":"text","text":"agentId:abc"},{"type":"text","text":"task result"}]}]},"tool_use_result":{"agentId":"abc","totalDurationMs":5000,"totalTokens":100,"totalToolUseCount":3}}`},
			},
		}
		blocks := resp.ExtractBlocks()
		if len(blocks) != 2 {
			t.Fatalf("got %d blocks, want 2", len(blocks))
		}
		task := blocks[0]
		if !task.IsTask {
			t.Error("expected IsTask=true")
		}
		if task.TaskDescription != "test" {
			t.Errorf("TaskDescription = %q, want 'test'", task.TaskDescription)
		}
		if len(task.TaskSubBlocks) != 2 {
			t.Fatalf("got %d sub-blocks, want 2", len(task.TaskSubBlocks))
		}
		if task.TaskMeta == nil || task.TaskMeta.AgentID != "abc" {
			t.Errorf("TaskMeta = %+v, want AgentID='abc'", task.TaskMeta)
		}
		if blocks[1].ToolOutput != "task result" {
			t.Errorf("task result = %q, want 'task result'", blocks[1].ToolOutput)
		}
	})

	t.Run("empty events", func(t *testing.T) {
		resp := &Response{}
		if blocks := resp.ExtractBlocks(); len(blocks) != 0 {
			t.Errorf("got %d blocks, want 0", len(blocks))
		}
	})
}

func TestPrettyJSON(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want string
	}{
		{"valid JSON", []byte(`{"key":"value"}`), "{\n  \"key\": \"value\"\n}"},
		{"invalid JSON", []byte(`not json`), "not json"},
		{"empty object", []byte(`{}`), "{}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PrettyJSON(tt.raw)
			if got != tt.want {
				t.Errorf("PrettyJSON(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{500, "500"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{45200, "45.2k"},
		{1000000, "1.0M"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := FormatTokens(tt.n)
			if got != tt.want {
				t.Errorf("FormatTokens(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestToolInputSummary(t *testing.T) {
	tests := []struct {
		name   string
		tool   string
		input  string
		maxLen int
		want   string
	}{
		{"Bash command", "Bash", `{"command":"ls -la"}`, 50, "ls -la"},
		{"Read file", "Read", `{"file_path":"/tmp/test.go"}`, 50, "/tmp/test.go"},
		{"Grep pattern", "Grep", `{"pattern":"TODO"}`, 50, "TODO"},
		{"invalid JSON", "Bash", `not json`, 50, "not json"},
		{"unknown tool with common field", "Custom", `{"query":"search term"}`, 50, "search term"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToolInputSummary(tt.tool, tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCleanToolOutput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "hello", "hello"},
		{"tool_use_error tags", "<tool_use_error>permission denied</tool_use_error>", "permission denied"},
		{"whitespace", "  hello  ", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanToolOutput(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAssistantText(t *testing.T) {
	t.Run("from result", func(t *testing.T) {
		resp := &Response{Result: Result{ResultText: "final answer"}}
		if got := resp.AssistantText(); got != "final answer" {
			t.Errorf("got %q, want 'final answer'", got)
		}
	})

	t.Run("from assistant events", func(t *testing.T) {
		resp := &Response{
			Events: []StreamEvent{
				{Type: "assistant", Raw: `{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`},
			},
		}
		if got := resp.AssistantText(); got != "hello" {
			t.Errorf("got %q, want 'hello'", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		resp := &Response{}
		if got := resp.AssistantText(); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}
