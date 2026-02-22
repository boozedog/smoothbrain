package claudecode

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractBlocks parses all assistant and tool_result events into an ordered list of ChatBlocks.
// Subagent events (parent_tool_use_id set) are grouped into their parent Task block's TaskSubBlocks.
func (r *Response) ExtractBlocks() []ChatBlock {
	var blocks []ChatBlock

	for _, ev := range r.Events {
		parentID := ExtractParentToolUseID(ev.Raw)

		switch ev.Type {
		case "assistant":
			var msg struct {
				Message struct {
					Content []ContentBlock `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(ev.Raw), &msg) != nil {
				continue
			}

			if parentID != "" {
				// Subagent assistant event — route tool_use blocks to parent Task
				for _, block := range msg.Message.Content {
					if block.Type == "tool_use" {
						inputStr := "{}"
						if len(block.Input) > 0 {
							inputStr = PrettyJSON(block.Input)
						}
						if idx := FindTaskBlockIndex(blocks, parentID); idx >= 0 {
							blocks[idx].TaskSubBlocks = append(blocks[idx].TaskSubBlocks, ChatBlock{
								Kind:      BlockToolUse,
								ToolName:  block.Name,
								ToolID:    block.ID,
								ToolInput: inputStr,
							})
						}
					}
				}
				continue
			}

			// Top-level assistant event
			for _, block := range msg.Message.Content {
				switch block.Type {
				case "text":
					if block.Text != "" {
						blocks = append(blocks, ChatBlock{Kind: BlockText, Text: block.Text})
					}
				case "tool_use":
					inputStr := "{}"
					if len(block.Input) > 0 {
						inputStr = PrettyJSON(block.Input)
					}
					cb := ChatBlock{
						Kind:      BlockToolUse,
						ToolName:  block.Name,
						ToolID:    block.ID,
						ToolInput: inputStr,
					}
					if block.Name == "Task" {
						cb.IsTask = true
						ParseTaskInput(&cb, inputStr)
					}
					blocks = append(blocks, cb)
				}
			}

		case "content_block_start":
			var cbs struct {
				ContentBlock ContentBlock `json:"content_block"`
			}
			if json.Unmarshal([]byte(ev.Raw), &cbs) == nil && cbs.ContentBlock.Type == "tool_use" {
				inputStr := "{}"
				if len(cbs.ContentBlock.Input) > 0 {
					inputStr = PrettyJSON(cbs.ContentBlock.Input)
				}
				cb := ChatBlock{
					Kind:      BlockToolUse,
					ToolName:  cbs.ContentBlock.Name,
					ToolID:    cbs.ContentBlock.ID,
					ToolInput: inputStr,
				}
				if cbs.ContentBlock.Name == "Task" {
					cb.IsTask = true
				}
				blocks = append(blocks, cb)
			}

		case "user":
			var msg struct {
				Message struct {
					Content []struct {
						Type      string `json:"type"`
						ToolUseID string `json:"tool_use_id"`
						Content   any    `json:"content"`
						IsError   bool   `json:"is_error"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(ev.Raw), &msg) != nil {
				continue
			}

			if parentID != "" {
				// Subagent user event — route tool_result blocks to parent Task
				for _, block := range msg.Message.Content {
					if block.Type == "tool_result" {
						output := ExtractToolResultContent(block.Content, false)
						if idx := FindTaskBlockIndex(blocks, parentID); idx >= 0 {
							blocks[idx].TaskSubBlocks = append(blocks[idx].TaskSubBlocks, ChatBlock{
								Kind:       BlockToolResult,
								ToolID:     block.ToolUseID,
								ToolOutput: output,
								IsError:    block.IsError,
							})
						}
					}
				}
				continue
			}

			// Top-level user event
			for _, block := range msg.Message.Content {
				if block.Type == "tool_result" {
					var output string
					if idx := FindTaskBlockIndex(blocks, block.ToolUseID); idx >= 0 {
						// Task result — strip agentId block and parse metadata
						output = ExtractToolResultContent(block.Content, true)
						blocks[idx].TaskMeta = ParseToolUseResult(ev.Raw)
					} else {
						output = ExtractToolResultContent(block.Content, false)
					}
					blocks = append(blocks, ChatBlock{
						Kind:       BlockToolResult,
						ToolID:     block.ToolUseID,
						ToolOutput: output,
						IsError:    block.IsError,
					})
				}
			}
		}
	}

	return blocks
}

// AssistantText extracts the text content from assistant events.
func (r *Response) AssistantText() string {
	// Prefer result.result if present
	if r.Result.ResultText != "" {
		return r.Result.ResultText
	}
	// Fall back to extracting from assistant events
	var last string
	for _, ev := range r.Events {
		if ev.Type == "assistant" {
			var msg struct {
				Message struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(ev.Raw), &msg) == nil {
				for _, block := range msg.Message.Content {
					if block.Type == "text" && block.Text != "" {
						last = block.Text
					}
				}
			}
		}
	}
	return last
}

// ToolInputSummary extracts a meaningful one-line summary from a tool's JSON input.
func ToolInputSummary(toolName, jsonInput string, maxLen int) string {
	var fields map[string]any
	if json.Unmarshal([]byte(jsonInput), &fields) != nil {
		return truncateString(jsonInput, maxLen)
	}

	var summary string
	switch toolName {
	case "Bash":
		summary, _ = fields["command"].(string)
	case "Read", "Write", "Edit":
		summary, _ = fields["file_path"].(string)
	case "Glob":
		if p, ok := fields["pattern"].(string); ok {
			summary = p
			if path, ok := fields["path"].(string); ok {
				summary = path + "/" + p
			}
		}
	case "Grep":
		summary, _ = fields["pattern"].(string)
	case "WebFetch":
		summary, _ = fields["url"].(string)
	default:
		for _, key := range []string{"command", "file_path", "path", "pattern", "query", "url", "prompt"} {
			if v, ok := fields[key].(string); ok && v != "" {
				summary = v
				break
			}
		}
	}

	if summary == "" {
		return truncateString(jsonInput, maxLen)
	}
	return truncateString(strings.TrimSpace(summary), maxLen)
}

// CleanToolOutput cleans up tool output for display, stripping XML error tags.
func CleanToolOutput(s string) string {
	s = strings.TrimSpace(s)
	if after, ok := strings.CutPrefix(s, "<tool_use_error>"); ok {
		s = strings.TrimSuffix(after, "</tool_use_error>")
		s = strings.TrimSpace(s)
	}
	// Strip cat-n style prefix from first line (e.g., "     1→")
	if idx := strings.Index(s, "→"); idx >= 0 && idx < 12 {
		prefix := strings.TrimSpace(s[:idx])
		if _, err := fmt.Sscanf(prefix, "%d", new(int)); err == nil {
			s = strings.TrimSpace(s[idx+len("→"):])
		}
	}
	return s
}

// FormatTokens formats a token count for display (e.g. 1500 → "1.5k").
func FormatTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// truncateString truncates s to maxLen runes, appending "..." if truncated.
func truncateString(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}
