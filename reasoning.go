package main

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// isAssistantOutputItem reports whether a Responses input item belongs to an
// assistant turn (what DeepSeek thinking mode requires reasoning_text for).
func isAssistantOutputItem(item gjson.Result) bool {
	switch item.Get("type").String() {
	case "reasoning", "function_call", "custom_tool_call", "local_shell_call", "computer_call", "web_search_call", "tool_search_call":
		return true
	case "message", "":
		return item.Get("role").String() == "assistant"
	}
	return false
}

func reasoningItemText(item gjson.Result) string {
	var parts []string
	for _, part := range item.Get("content").Array() {
		if part.Get("type").String() == "reasoning_text" {
			if t := part.Get("text").String(); strings.TrimSpace(t) != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func reasoningItemSummaryText(item gjson.Result) string {
	var parts []string
	for _, part := range item.Get("summary").Array() {
		if t := part.Get("text").String(); strings.TrimSpace(t) != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

func newReasoningItem(text string) string {
	item := `{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text"}]}`
	item, _ = sjson.Set(item, "content.0.text", text)
	return item
}

// ensureReasoningPassback guarantees every replayed assistant turn carries a
// type=reasoning item with a non-empty reasoning_text part. Existing
// reasoning text is retained verbatim; empty ones are filled from the summary
// text or the placeholder. Reasoning items are never removed. Returns the
// body and whether it changed.
func ensureReasoningPassback(body []byte, placeholder string) ([]byte, bool) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false
	}
	if strings.TrimSpace(placeholder) == "" {
		placeholder = defaultReasoningPlaceholder
	}
	items := input.Array()
	out := make([]string, 0, len(items)+4)
	changed := false
	i := 0
	for i < len(items) {
		if !isAssistantOutputItem(items[i]) {
			out = append(out, items[i].Raw)
			i++
			continue
		}
		j := i
		for j < len(items) && isAssistantOutputItem(items[j]) {
			j++
		}
		run := items[i:j]
		hasReasoning := false
		for _, it := range run {
			if it.Get("type").String() == "reasoning" {
				hasReasoning = true
				break
			}
		}
		if !hasReasoning {
			out = append(out, newReasoningItem(placeholder))
			changed = true
		}
		for _, it := range run {
			if it.Get("type").String() != "reasoning" {
				out = append(out, it.Raw)
				continue
			}
			if reasoningItemText(it) != "" {
				out = append(out, it.Raw)
				continue
			}
			text := reasoningItemSummaryText(it)
			if text == "" {
				text = placeholder
			}
			raw, err := sjson.SetRaw(it.Raw, "content", `[{"type":"reasoning_text"}]`)
			if err == nil {
				raw, err = sjson.Set(raw, "content.0.text", text)
			}
			if err != nil {
				out = append(out, it.Raw)
				continue
			}
			out = append(out, raw)
			changed = true
		}
		i = j
	}
	if !changed {
		return body, false
	}
	updated, err := sjson.SetRawBytes(body, "input", []byte("["+strings.Join(out, ",")+"]"))
	if err != nil {
		return body, false
	}
	return updated, true
}

// expandCapsules replaces clc-v1 compaction items by a user context message and
// drops foreign compaction items (e.g. GPT opaque blobs this model cannot read).
func expandCapsules(body []byte) ([]byte, bool) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false
	}
	items := input.Array()
	out := make([]string, 0, len(items))
	changed := false
	for _, it := range items {
		if it.Get("type").String() != "compaction" {
			out = append(out, it.Raw)
			continue
		}
		changed = true
		summary, ok := expandCapsule(strings.TrimSpace(it.Get("encrypted_content").String()))
		if !ok {
			continue // foreign/undecodable compaction item: drop, it cannot be replayed here
		}
		msg := `{"type":"message","role":"user","content":[{"type":"input_text"}]}`
		msg, _ = sjson.Set(msg, "content.0.text", "Context summary from previous turns:\n"+summary)
		out = append(out, msg)
	}
	if !changed {
		return body, false
	}
	updated, err := sjson.SetRawBytes(body, "input", []byte("["+strings.Join(out, ",")+"]"))
	if err != nil {
		return body, false
	}
	return updated, true
}

func hasCompactionTrigger(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	for _, it := range input.Array() {
		if it.Get("type").String() == "compaction_trigger" {
			return true
		}
	}
	return false
}

func hasCompactionItem(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	for _, it := range input.Array() {
		if it.Get("type").String() == "compaction" {
			return true
		}
	}
	return false
}
