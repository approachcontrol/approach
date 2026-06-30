// Package claudestream renders the JSONL event stream emitted by
// `claude --print --verbose --output-format stream-json --include-partial-messages`
// into readable terminal lines for the wtui embedded terminal. Claude's print
// mode offers no human-readable streaming format (text output is buffered until
// completion), so headless launches stream stream-json and translate each event
// here.
//
// With --include-partial-messages, claude emits low-level `stream_event` deltas
// (token-by-token text, incremental tool-call arguments) in addition to the
// aggregated `assistant` events. The renderer streams from the deltas and
// suppresses the aggregated `assistant` events to avoid double-printing; if no
// deltas are seen (older claude, flag ignored) it falls back to rendering the
// aggregated events.
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
// partial lines across calls and tracks the in-flight streamed content block.
// Renderer is not safe for concurrent use; the embedded terminal read loop is
// the single writer.
type Renderer struct {
	buf []byte

	partial   bool   // a stream_event was seen; suppress aggregated assistant events
	blockType string // type of the streaming content block: text/thinking/tool_use
	toolName  string // tool name for the streaming tool_use block
	toolArgs  []byte // accumulated input_json_delta fragments
	textOpen  bool   // a streamed text line is open and needs a closing newline
}

// NewRenderer returns a Renderer with empty state.
func NewRenderer() *Renderer { return &Renderer{} }

// Transform consumes a chunk of child output and returns the readable bytes to
// write to the terminal emulator. Complete lines are parsed as stream-json
// events and rendered; a trailing partial line is held until the next call.
// When final is true (child EOF), any buffered remainder is flushed and an open
// streamed text line is closed.
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
		out = append(out, r.renderLine(line)...)
	}
	if final {
		if len(bytes.TrimSpace(r.buf)) > 0 {
			out = append(out, r.renderLine(r.buf)...)
		}
		r.buf = nil
		if r.textOpen {
			out = append(out, '\r', '\n')
			r.textOpen = false
		}
	}
	return out
}

type streamEvent struct {
	Type           string        `json:"type"`
	Subtype        string        `json:"subtype"`
	Model          string        `json:"model"`
	PermissionMode string        `json:"permissionMode"`
	Message        streamMessage `json:"message"`
	IsError        bool          `json:"is_error"`
	DurationMs     int64         `json:"duration_ms"`
	NumTurns       int           `json:"num_turns"`
	TotalCostUSD   float64       `json:"total_cost_usd"`
	Event          *innerEvent   `json:"event"`
}

type innerEvent struct {
	Type         string       `json:"type"`
	ContentBlock *streamBlock `json:"content_block"`
	Delta        *streamDelta `json:"delta"`
}

type streamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	PartialJSON string `json:"partial_json"`
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

func (r *Renderer) renderLine(raw []byte) []byte {
	line := bytes.TrimSpace(raw)
	if len(line) == 0 {
		return nil
	}
	// Anything that is not a JSON object (stray warnings, auth errors, "trust
	// this folder" prompts) is surfaced verbatim so failures stay visible.
	if line[0] != '{' {
		return crlf(string(line))
	}
	var ev streamEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return crlf(string(line))
	}
	return r.renderEvent(ev)
}

func (r *Renderer) renderEvent(ev streamEvent) []byte {
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" {
			return joinLines(renderInit(ev))
		}
		return nil
	case "assistant":
		if r.partial {
			// Already streamed token-by-token via stream_event deltas. This
			// relies on a message's stream_event deltas (message_start, etc.)
			// always preceding its aggregated assistant event, which the
			// Anthropic stream-json ordering guarantees; otherwise the first
			// assistant block would render twice.
			return nil
		}
		return joinLines(renderBlocks(ev.Message.Content))
	case "user":
		return joinLines(renderBlocks(ev.Message.Content))
	case "result":
		return joinLines(renderResult(ev))
	case "stream_event":
		if ev.Event == nil {
			return nil
		}
		return r.renderStreamEvent(*ev.Event)
	default:
		// rate_limit_event and anything unknown are dropped.
		return nil
	}
}

// renderStreamEvent handles a partial-message delta. It tracks a single
// in-flight content block (claude streams blocks sequentially — block N fully
// starts/deltas/stops before block N+1 — so the block `index` is not needed).
func (r *Renderer) renderStreamEvent(ev innerEvent) []byte {
	r.partial = true
	switch ev.Type {
	case "message_start":
		r.resetBlock()
	case "content_block_start":
		r.resetBlock()
		if ev.ContentBlock == nil {
			return nil
		}
		switch ev.ContentBlock.Type {
		case "text":
			r.blockType = "text"
		case "thinking":
			r.blockType = "thinking"
			return crlf("✻ Thinking…")
		case "tool_use":
			r.blockType = "tool_use"
			r.toolName = ev.ContentBlock.Name
		}
	case "content_block_delta":
		if ev.Delta == nil {
			return nil
		}
		switch ev.Delta.Type {
		case "text_delta":
			if r.blockType == "text" && ev.Delta.Text != "" {
				r.textOpen = true
				return []byte(normalizeCRLF(ev.Delta.Text))
			}
		case "input_json_delta":
			if r.blockType == "tool_use" {
				r.toolArgs = append(r.toolArgs, ev.Delta.PartialJSON...)
			}
		}
	case "content_block_stop":
		return r.closeBlock()
	}
	return nil
}

func (r *Renderer) closeBlock() []byte {
	var out []byte
	switch r.blockType {
	case "text":
		if r.textOpen {
			out = crlf("")
		}
	case "tool_use":
		out = crlf("⏺ " + r.toolName + "(" + summarizeToolInput(r.toolArgs) + ")")
	}
	r.resetBlock()
	return out
}

func (r *Renderer) resetBlock() {
	r.blockType = ""
	r.toolName = ""
	r.toolArgs = nil
	r.textOpen = false
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
	if len(bytes.TrimSpace(raw)) == 0 {
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

// crlf returns the line followed by CRLF so the vt emulator returns the carriage
// to column zero on each newline.
func crlf(s string) []byte {
	return append([]byte(s), '\r', '\n')
}

func joinLines(lines []string) []byte {
	var out []byte
	for _, line := range lines {
		out = append(out, line...)
		out = append(out, '\r', '\n')
	}
	return out
}

// normalizeCRLF rewrites any newline style in streamed text to CRLF.
func normalizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
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
