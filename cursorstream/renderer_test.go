package cursorstream_test

import (
	"strings"
	"testing"

	"github.com/approachcontrol/approach/cursorstream"
)

const sampleEvents = `{"type":"system","subtype":"init","model":"composer-2.5"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Looking at the repo.\n"}]}}
{"type":"tool_call","subtype":"started","message":{"content":[{"name":"ReadFile"}]}}
{"type":"assistant","timestamp_ms":1,"message":{"content":[{"type":"text","text":"notes"}]}}
{"type":"assistant","timestamp_ms":2,"message":{"content":[{"type":"text","text":".txt has 2 lines."}]}}
{"type":"result","subtype":"success"}
`

func renderAll(t *testing.T, input string) string {
	t.Helper()
	r := cursorstream.NewRenderer()
	return string(r.Transform([]byte(input), true))
}

func TestRendererRendersReadableEventStream(t *testing.T) {
	got := renderAll(t, sampleEvents)
	if !strings.Contains(got, "composer-2.5") {
		t.Errorf("missing model header, got:\n%s", got)
	}
	if !strings.Contains(got, "Looking at the repo.") {
		t.Errorf("missing assistant text, got:\n%s", got)
	}
	if !strings.Contains(got, "ReadFile") {
		t.Errorf("missing tool call, got:\n%s", got)
	}
	if !strings.Contains(got, "notes.txt has 2 lines.") {
		t.Errorf("missing streamed assistant text, got:\n%s", got)
	}
	if !strings.Contains(got, "done") {
		t.Errorf("missing result marker, got:\n%s", got)
	}
	if strings.Contains(got, `"type":`) || strings.Contains(got, "timestamp_ms") {
		t.Errorf("raw JSON leaked into output:\n%s", got)
	}
	if strings.Contains(got, "\n") && !strings.Contains(got, "\r\n") {
		t.Errorf("expected CRLF line endings, got:\n%q", got)
	}
}

func TestRendererHandlesChunkedInputAcrossCalls(t *testing.T) {
	r := cursorstream.NewRenderer()
	data := []byte(sampleEvents)
	var b strings.Builder
	for i := 0; i < len(data); i++ {
		b.Write(r.Transform(data[i:i+1], i == len(data)-1))
	}
	got := b.String()
	if !strings.Contains(got, "composer-2.5") {
		t.Errorf("chunked input dropped the model header, got:\n%s", got)
	}
	if !strings.Contains(got, "notes.txt has 2 lines.") {
		t.Errorf("chunked input dropped streamed text, got:\n%s", got)
	}
}

func TestRendererSurfacesNonJSONFailures(t *testing.T) {
	got := renderAll(t, "Not logged in.\n")
	if !strings.Contains(got, "Not logged in.") {
		t.Errorf("non-JSON output should be surfaced, got:\n%s", got)
	}
}

func TestRendererClosesStreamedTextBeforeToolCall(t *testing.T) {
	got := renderAll(t, `{"type":"assistant","timestamp_ms":1,"message":{"content":[{"type":"text","text":"I'll read"}]}}
{"type":"tool_call","subtype":"started","message":{"content":[{"name":"ReadFile"}]}}
`)
	if strings.Contains(got, "I'll read⏺") || strings.Contains(got, "I'll read⏺ ReadFile") {
		t.Fatalf("streamed text merged into the tool line:\n%q", got)
	}
	if !strings.Contains(got, "I'll read\r\n") {
		t.Fatalf("expected streamed text on its own line, got:\n%q", got)
	}
	if !strings.Contains(got, "⏺ ReadFile\r\n") {
		t.Fatalf("expected tool marker on its own line, got:\n%q", got)
	}
}
