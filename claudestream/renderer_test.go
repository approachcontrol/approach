package claudestream_test

import (
	"strings"
	"testing"

	"github.com/brian-bell/wtui/claudestream"
)

// sampleEvents mirrors the JSONL shape emitted by
// `claude --print --verbose --output-format stream-json`.
const sampleEvents = `{"type":"system","subtype":"init","cwd":"/repo/worktree","model":"claude-opus-4-8","permissionMode":"auto","tools":["Bash","Edit"]}
{"type":"rate_limit_event","rate_limit_info":{},"session_id":"abc"}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":""}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"wc -l notes.txt","description":"Count lines"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":false,"content":"       2 notes.txt"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"notes.txt has 2 lines."}]}}
{"type":"result","subtype":"success","is_error":false,"duration_ms":5670,"num_turns":2,"result":"notes.txt has 2 lines.","total_cost_usd":0.1414}
`

func renderAll(t *testing.T, input string) string {
	t.Helper()
	r := claudestream.NewRenderer()
	out := r.Transform([]byte(input), true)
	return string(out)
}

func TestRendererRendersReadableEventStream(t *testing.T) {
	got := renderAll(t, sampleEvents)

	// Tool call shows the tool name and its key argument.
	if !strings.Contains(got, "Bash(wc -l notes.txt)") {
		t.Errorf("missing readable tool call line, got:\n%s", got)
	}
	// Tool result is surfaced (at least its first line).
	if !strings.Contains(got, "2 notes.txt") {
		t.Errorf("missing tool result, got:\n%s", got)
	}
	// Final assistant text is surfaced verbatim.
	if !strings.Contains(got, "notes.txt has 2 lines.") {
		t.Errorf("missing assistant text, got:\n%s", got)
	}
	// The init header surfaces the model.
	if !strings.Contains(got, "claude-opus-4-8") {
		t.Errorf("missing model header, got:\n%s", got)
	}
	// Raw JSON must never leak into the panel.
	if strings.Contains(got, `"type":`) || strings.Contains(got, "tool_use") {
		t.Errorf("raw JSON leaked into output:\n%s", got)
	}
	// Result summary surfaces turns/cost.
	if !strings.Contains(got, "2 turns") {
		t.Errorf("missing result turn count, got:\n%s", got)
	}
	// Noise events are dropped.
	if strings.Contains(got, "rate_limit") {
		t.Errorf("rate_limit_event should be dropped, got:\n%s", got)
	}
	// Lines must be CRLF-terminated so the vt emulator returns the carriage.
	if strings.Contains(got, "\n") && !strings.Contains(got, "\r\n") {
		t.Errorf("expected CRLF line endings, got:\n%q", got)
	}
}

func TestRendererHandlesChunkedInputAcrossCalls(t *testing.T) {
	r := claudestream.NewRenderer()
	data := []byte(sampleEvents)
	var b strings.Builder
	// Feed one byte at a time to prove line reassembly across Transform calls.
	for i := 0; i < len(data); i++ {
		final := i == len(data)-1
		b.Write(r.Transform(data[i:i+1], final))
	}
	got := b.String()
	if !strings.Contains(got, "Bash(wc -l notes.txt)") {
		t.Errorf("chunked input dropped the tool call, got:\n%s", got)
	}
	if !strings.Contains(got, "notes.txt has 2 lines.") {
		t.Errorf("chunked input dropped assistant text, got:\n%s", got)
	}
}

func TestRendererPassesThroughNonJSONLines(t *testing.T) {
	got := renderAll(t, "Error: not logged in\n")
	if !strings.Contains(got, "Error: not logged in") {
		t.Errorf("non-JSON line should pass through, got:\n%q", got)
	}
}

func TestRendererPassesThroughMalformedJSON(t *testing.T) {
	// Starts with '{' but is not valid JSON: must surface verbatim, not vanish.
	got := renderAll(t, `{"type":"assistant" BROKEN`+"\n")
	if !strings.Contains(got, "BROKEN") {
		t.Errorf("malformed JSON should pass through verbatim, got:\n%q", got)
	}
}

func TestRendererFlushesTrailingPartialLineOnFinal(t *testing.T) {
	r := claudestream.NewRenderer()
	// No trailing newline; the renderer must still flush on final=true.
	out := r.Transform([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}`), true)
	if !strings.Contains(string(out), "done") {
		t.Errorf("trailing partial line not flushed, got:\n%q", string(out))
	}
}

func TestRendererErrorResultMarked(t *testing.T) {
	got := renderAll(t, `{"type":"result","subtype":"error_during_execution","is_error":true,"duration_ms":1200}`+"\n")
	if !strings.Contains(got, "error_during_execution") {
		t.Errorf("error result should surface subtype, got:\n%s", got)
	}
}

func TestRendererToolResultErrorMarked(t *testing.T) {
	got := renderAll(t, `{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"boom"}]}}`+"\n")
	if !strings.Contains(got, "boom") {
		t.Errorf("error tool result content should surface, got:\n%s", got)
	}
}

// partialEvents mirrors `--include-partial-messages`: stream_event deltas arrive
// alongside the aggregated assistant events that must be suppressed.
const partialEvents = `{"type":"system","subtype":"init","model":"claude-opus-4-8","permissionMode":"auto"}
{"type":"stream_event","event":{"type":"message_start"}}
{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"x"}}}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":""}]}}
{"type":"stream_event","event":{"type":"content_block_stop","index":0}}
{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"Bash"}}}
{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\": \"cat notes.txt"}}}
{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\", \"description\": \"read\"}"}}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"cat notes.txt","description":"read"}}]}}
{"type":"stream_event","event":{"type":"content_block_stop","index":1}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":false,"content":"wtui is a worktree TUI."}]}}
{"type":"stream_event","event":{"type":"message_start"}}
{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text"}}}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"notes.txt "}}}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"contains a single line."}}}
{"type":"assistant","message":{"content":[{"type":"text","text":"notes.txt contains a single line."}]}}
{"type":"stream_event","event":{"type":"content_block_stop","index":0}}
{"type":"result","subtype":"success","is_error":false,"duration_ms":4000,"num_turns":2,"total_cost_usd":0.1}
`

func TestRendererStreamsPartialMessagesWithoutDuplication(t *testing.T) {
	got := renderAll(t, partialEvents)

	// Streamed text deltas reassemble into the final answer, exactly once: the
	// aggregated assistant text event must be suppressed in partial mode.
	if n := strings.Count(got, "notes.txt contains a single line."); n != 1 {
		t.Errorf("assistant text rendered %d times, want exactly 1, got:\n%s", n, got)
	}
	// The tool call is rendered once from the streamed input_json_delta, not
	// also from the aggregated assistant event.
	if n := strings.Count(got, "Bash(cat notes.txt)"); n != 1 {
		t.Errorf("tool call rendered %d times, want exactly 1, got:\n%s", n, got)
	}
	if !strings.Contains(got, "✻ Thinking…") {
		t.Errorf("missing thinking marker, got:\n%s", got)
	}
	if !strings.Contains(got, "wtui is a worktree TUI.") {
		t.Errorf("missing tool result, got:\n%s", got)
	}
	if !strings.Contains(got, "2 turns") {
		t.Errorf("missing result summary, got:\n%s", got)
	}
	if strings.Contains(got, `"type":`) || strings.Contains(got, "stream_event") {
		t.Errorf("raw JSON leaked into output:\n%s", got)
	}
}

func TestRendererStreamsPartialTextAcrossChunks(t *testing.T) {
	r := claudestream.NewRenderer()
	data := []byte(partialEvents)
	var b strings.Builder
	for i := 0; i < len(data); i++ {
		b.Write(r.Transform(data[i:i+1], i == len(data)-1))
	}
	got := b.String()
	if n := strings.Count(got, "notes.txt contains a single line."); n != 1 {
		t.Errorf("byte-chunked partial stream rendered text %d times, want 1, got:\n%s", n, got)
	}
	if n := strings.Count(got, "Bash(cat notes.txt)"); n != 1 {
		t.Errorf("byte-chunked partial stream rendered tool call %d times, want 1, got:\n%s", n, got)
	}
}
