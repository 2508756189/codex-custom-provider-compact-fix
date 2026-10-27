package main

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestReasoningPassback(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4","input":[
{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
{"type":"function_call","call_id":"c1","name":"sh","arguments":"{}"},
{"type":"function_call_output","call_id":"c1","output":"ok"},
{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"keep me"}]},
{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]},
{"type":"message","role":"user","content":[{"type":"input_text","text":"more"}]},
{"type":"reasoning","summary":[],"content":[]},
{"type":"message","role":"assistant","content":[{"type":"output_text","text":"x"}]}]}`)
	out, ch := ensureReasoningPassback(body, "")
	if !ch {
		t.Fatal("expected change")
	}
	in := gjson.GetBytes(out, "input").Array()
	if in[1].Get("type").String() != "reasoning" || in[1].Get("content.0.text").String() != defaultReasoningPlaceholder {
		t.Fatalf("missing inserted reasoning: %s", in[1].Raw)
	}
	if !strings.Contains(string(out), "keep me") {
		t.Fatal("lost retained reasoning")
	}
	last := 0
	for _, it := range in {
		if it.Get("type").String() == "reasoning" {
			last++
			if it.Get("content.0.text").String() == "" {
				t.Fatalf("empty reasoning_text: %s", it.Raw)
			}
		}
	}
	if last != 3 {
		t.Fatalf("reasoning items = %d", last)
	}
	out2, ch2 := ensureReasoningPassback(out, "")
	if ch2 || string(out2) != string(out) {
		t.Fatal("not idempotent")
	}
}

func TestCapsuleRoundtrip(t *testing.T) {
	c, err := sealCapsule("sum", "m")
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := expandCapsule(c); !ok || s != "sum" {
		t.Fatal("roundtrip")
	}
	body := []byte(`{"input":[{"type":"compaction","encrypted_content":"` + c + `"},{"type":"compaction","encrypted_content":"gAAAA"}]}`)
	out, _ := expandCapsules(body)
	if gjson.GetBytes(out, "input.#").Int() != 1 || !strings.Contains(string(out), "Context summary") {
		t.Fatalf("%s", out)
	}
}

func TestAllow(t *testing.T) {
	if !modelAllowed("deepseek-v4.1-flash") || modelAllowed("gpt-5.5") || modelAllowed("grok-4") {
		t.Fatal("allowlist")
	}
}

func TestSummaryBody(t *testing.T) {
	body := []byte(`{"model":"deepseek-x","stream":true,"tools":[{"type":"function"}],"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},{"type":"function_call","name":"sh","arguments":"{}","call_id":"1"},{"type":"function_call_output","call_id":"1","output":"ok"},{"type":"compaction_trigger"}]}`)
	out := prepareSummaryBody(body, "deepseek-x")
	if gjson.GetBytes(out, "tools").Exists() || gjson.GetBytes(out, "stream").Bool() || strings.Contains(string(out), "compaction_trigger") || strings.Contains(string(out), `"function_call"`) {
		t.Fatalf("%s", out)
	}
}
