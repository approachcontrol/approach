// Package cursorstream renders the JSONL event stream emitted by
// `cursor-agent -p --output-format stream-json --stream-partial-output`
// into readable terminal lines for the approach embedded terminal. Cursor's
// print mode text format waits for the final answer, so headless launches
// stream stream-json and translate each event here.
package cursorstream

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Renderer converts a stream of stream-json bytes into readable terminal lines.
// It is stateful: callers feed arbitrary byte chunks to Transform, which buffers
// partial lines across calls. Renderer is not safe for concurrent use; the
// embedded terminal read loop is the single writer.
type Renderer struct {
	buf      []byte
	textOpen bool
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
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	Model     string          `json:"model"`
	Message   streamMessage   `json:"message"`
	Timestamp json.RawMessage `json:"timestamp_ms"`
}

type streamMessage struct {
	Content []streamBlock `json:"content"`
}

type streamBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

func (r *Renderer) renderLine(raw []byte) []byte {
	line := bytes.TrimSpace(raw)
	if len(line) == 0 {
		return nil
	}
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
			label := strings.TrimSpace(ev.Model)
			if label == "" {
				label = "cursor-agent"
			}
			return crlf("● " + label)
		}
		return nil
	case "assistant":
		text := assistantText(ev.Message.Content)
		if text == "" {
			return nil
		}
		if len(ev.Timestamp) > 0 {
			r.textOpen = true
			return []byte(normalizeCRLF(text))
		}
		if r.textOpen {
			out := crlf("")
			r.textOpen = false
			out = append(out, crlf(strings.TrimRight(text, "\n"))...)
			return out
		}
		return joinLines(splitTextLines(text))
	case "tool_call":
		name := firstToolName(ev)
		if name == "" {
			name = "tool"
		}
		if r.textOpen {
			out := crlf("")
			r.textOpen = false
			return append(out, crlf("⏺ "+name)...)
		}
		return crlf("⏺ " + name)
	case "result":
		if r.textOpen {
			out := crlf("")
			r.textOpen = false
			return append(out, crlf("✔ done")...)
		}
		return crlf("✔ done")
	default:
		return nil
	}
}

func assistantText(blocks []streamBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" || b.Type == "" {
			if t := b.Text; t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "")
}

func firstToolName(ev streamEvent) string {
	for _, b := range ev.Message.Content {
		if name := strings.TrimSpace(b.Name); name != "" {
			return name
		}
	}
	return strings.TrimSpace(ev.Subtype)
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

func normalizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}
