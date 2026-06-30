// Package claudestream renders the JSONL event stream emitted by
// `claude --print --verbose --output-format stream-json` into readable
// terminal lines for the wtui embedded terminal. Claude's print mode offers no
// human-readable streaming format (text output is buffered until completion),
// so headless launches stream stream-json and translate each event here.
package claudestream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Renderer converts a stream of stream-json bytes into readable terminal lines.
// It is stateful: callers feed arbitrary byte chunks to Transform, which buffers
// partial lines across calls. Renderer is not safe for concurrent use; the
// embedded terminal read loop is the single writer.
type Renderer struct {
	buf []byte
}

// NewRenderer returns a Renderer with an empty line buffer.
func NewRenderer() *Renderer { return &Renderer{} }

// Transform consumes a chunk of child output and returns the readable bytes to
// write to the terminal emulator. Complete lines are parsed as stream-json
// events and rendered; a trailing partial line is held until the next call.
// When final is true (child EOF), any buffered remainder is flushed.
func (r *Renderer) Transform(p []byte, final bool) []byte {
	r.buf = append(r.buf, p...)
	var out []byte
	for {
		i := bytes.IndexByte(r.buf, '\n')
		if i < 0 {
			break
		}
		line := r.buf[:i]
		r.buf = r.buf[i+1:]
		out = appendLines(out, r.renderLine(line))
	}
	if final && len(bytes.TrimSpace(r.buf)) > 0 {
		out = appendLines(out, r.renderLine(r.buf))
		r.buf = nil
	}
	return out
}

// appendLines writes each rendered line followed by CRLF so the vt emulator
// returns the carriage to column zero on each newline.
func appendLines(out []byte, lines []string) []byte {
	for _, line := range lines {
		out = append(out, line...)
		out = append(out, '\r', '\n')
	}
	return out
}

type streamEvent struct {
	Type           string          `json:"type"`
	Subtype        string          `json:"subtype"`
	Model          string          `json:"model"`
	PermissionMode string          `json:"permissionMode"`
	Message        streamMessage   `json:"message"`
	IsError        bool            `json:"is_error"`
	DurationMs     int64           `json:"duration_ms"`
	NumTurns       int             `json:"num_turns"`
	TotalCostUSD   float64         `json:"total_cost_usd"`
	Result         json.RawMessage `json:"result"`
}

type streamMessage struct {
	Content []streamBlock `json:"content"`
}

type streamBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Content  json.RawMessage `json:"content"`
	IsError  bool            `json:"is_error"`
}

func (r *Renderer) renderLine(raw []byte) []string {
	line := bytes.TrimSpace(raw)
	if len(line) == 0 {
		return nil
	}
	// Anything that is not a JSON object (stray warnings, auth errors, "trust
	// this folder" prompts) is surfaced verbatim so failures stay visible.
	if line[0] != '{' {
		return []string{string(line)}
	}
	var ev streamEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return []string{string(line)}
	}
	return renderEvent(ev)
}

func renderEvent(ev streamEvent) []string {
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" {
			return renderInit(ev)
		}
		return nil
	case "assistant", "user":
		return renderBlocks(ev.Message.Content)
	case "result":
		return renderResult(ev)
	default:
		// rate_limit_event, stream_event, and anything unknown are dropped.
		return nil
	}
}

func renderInit(ev streamEvent) []string {
	label := strings.TrimSpace(ev.Model)
	if label == "" {
		label = "claude"
	}
	if mode := strings.TrimSpace(ev.PermissionMode); mode != "" {
		label += " · " + mode
	}
	return []string{"● " + label}
}

func renderBlocks(blocks []streamBlock) []string {
	var lines []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			lines = append(lines, splitTextLines(b.Text)...)
		case "thinking":
			lines = append(lines, "✻ Thinking…")
		case "tool_use":
			lines = append(lines, "⏺ "+b.Name+"("+summarizeToolInput(b.Input)+")")
		case "tool_result":
			lines = append(lines, renderToolResult(b))
		}
	}
	return lines
}

func splitTextLines(text string) []string {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	raw := strings.Split(text, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, strings.TrimRight(line, "\r"))
	}
	return lines
}

func renderToolResult(b streamBlock) string {
	content := firstLine(toolResultText(b.Content))
	prefix := "  ⎿ "
	if b.IsError {
		prefix = "  ⎿ ✗ "
	}
	if content == "" {
		content = "(no output)"
	}
	return prefix + truncate(content, 100)
}

// toolResultText flattens a tool_result content field, which may be a plain
// string or an array of {type:"text", text:...} blocks.
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []streamBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if t := strings.TrimSpace(b.Text); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// summarizeToolInput picks the most informative argument from a tool_use input
// object so a Bash call reads "Bash(wc -l notes.txt)" rather than a JSON blob.
func summarizeToolInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return ""
	}
	for _, key := range []string{"command", "file_path", "path", "pattern", "url", "query", "description", "prompt"} {
		if v, ok := input[key]; ok {
			if s := strings.TrimSpace(firstLine(stringify(v))); s != "" {
				return truncate(s, 60)
			}
		}
	}
	return ""
}

func renderResult(ev streamEvent) []string {
	marker := "✔ done"
	if ev.IsError {
		marker = "✗ " + nonEmpty(ev.Subtype, "error")
	} else if ev.Subtype != "" && ev.Subtype != "success" {
		marker = "✔ " + ev.Subtype
	}
	parts := []string{marker}
	if ev.NumTurns > 0 {
		parts = append(parts, fmt.Sprintf("%d turns", ev.NumTurns))
	}
	if ev.DurationMs > 0 {
		parts = append(parts, fmt.Sprintf("%.1fs", float64(ev.DurationMs)/1000))
	}
	if ev.TotalCostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", ev.TotalCostUSD))
	}
	return []string{strings.Join(parts, " · ")}
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, "\r")
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
