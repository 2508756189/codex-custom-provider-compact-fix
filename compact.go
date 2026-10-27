package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const defaultSummaryPrompt = "Please provide a concise and comprehensive summary of the preceding conversation and task progress so far, including user goals, key findings, actions taken, files and commands involved, decisions made, and current status, so that work can continue smoothly. Do not call tools."

func isResponsesFormat(f string) bool {
	switch strings.ToLower(strings.TrimSpace(f)) {
	case "openai-response", "openai-responses", "codex":
		return true
	}
	return false
}

type rpcRouteReq struct {
	pluginapi.ModelRouteRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func routeModel(raw []byte) ([]byte, error) {
	var req rpcRouteReq
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	decline := pluginapi.ModelRouteResponse{Handled: false}
	if !isResponsesFormat(req.SourceFormat) || !hasCompactionTrigger(req.Body) {
		return okEnvelope(decline)
	}
	model := strings.TrimSpace(req.RequestedModel)
	if model == "" {
		model = strings.TrimSpace(gjson.GetBytes(req.Body, "model").String())
	}
	if !modelAllowed(model) {
		return okEnvelope(decline)
	}
	return okEnvelope(pluginapi.ModelRouteResponse{
		Handled:    true,
		TargetKind: pluginapi.ModelRouteTargetSelf,
		Reason:     "codex_local_compact",
	})
}

type compactResult struct {
	JSON []byte
	SSE  [][]byte
}

// prepareSummaryBody builds the non-stream, tool-less summary request.
func prepareSummaryBody(src []byte, model string) []byte {
	out, _ := expandCapsules(src)
	input := gjson.GetBytes(out, "input")
	prompt := currentConfig().SummaryPrompt
	if strings.TrimSpace(prompt) == "" {
		prompt = defaultSummaryPrompt
	}
	promptMsg := `{"type":"message","role":"user","content":[{"type":"input_text"}]}`
	promptMsg, _ = sjson.Set(promptMsg, "content.0.text", prompt)

	items := []string{}
	switch {
	case input.IsArray():
		for _, it := range input.Array() {
			switch it.Get("type").String() {
			case "compaction_trigger":
				continue
			case "function_call", "custom_tool_call", "local_shell_call":
				name := it.Get("name").String()
				args := it.Get("arguments").String()
				if args == "" {
					args = it.Get("input").String()
				}
				items = append(items, textMessage("assistant", fmt.Sprintf("[tool call %s] %s", name, truncate(args, 4000))))
			case "function_call_output", "custom_tool_call_output":
				o := it.Get("output")
				text := o.String()
				if o.IsObject() || o.IsArray() {
					text = o.Raw
				}
				items = append(items, textMessage("user", "[tool result] "+truncate(text, 4000)))
			default:
				items = append(items, it.Raw)
			}
		}
	case input.Type == gjson.String && input.String() != "":
		items = append(items, textMessage("user", input.String()))
	}
	items = append(items, promptMsg)
	out, _ = sjson.SetRawBytes(out, "input", []byte("["+strings.Join(items, ",")+"]"))
	for _, f := range []string{"tools", "tool_choice", "previous_response_id", "parallel_tool_calls", "additional_tools", "truncation", "metadata", "include", "prompt_cache_key", "service_tier", "client_metadata"} {
		out, _ = sjson.DeleteBytes(out, f)
	}
	out, _ = sjson.SetBytes(out, "stream", false)
	out, _ = sjson.SetBytes(out, "store", false)
	out, _ = sjson.SetBytes(out, "model", model)
	// DeepSeek thinking-mode passback on the summary hop as well.
	out, _ = ensureReasoningPassback(out, currentConfig().ReasoningPlaceholder)
	return out
}

func textMessage(role, text string) string {
	typ := "input_text"
	if role == "assistant" {
		typ = "output_text"
	}
	msg := `{"type":"message","role":"","content":[{"type":""}]}`
	msg, _ = sjson.Set(msg, "role", role)
	msg, _ = sjson.Set(msg, "content.0.type", typ)
	msg, _ = sjson.Set(msg, "content.0.text", text)
	return msg
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}

func extractSummary(resp []byte) (string, error) {
	if st := strings.ToLower(gjson.GetBytes(resp, "status").String()); st != "" && st != "completed" {
		return "", fmt.Errorf("summary response status %q", st)
	}
	var parts []string
	for _, it := range gjson.GetBytes(resp, "output").Array() {
		if it.Get("type").String() != "message" {
			continue
		}
		c := it.Get("content")
		if c.IsArray() {
			for _, p := range c.Array() {
				if t := p.Get("type").String(); (t == "output_text" || t == "text") && p.Get("text").String() != "" {
					parts = append(parts, p.Get("text").String())
				}
			}
		} else if c.Type == gjson.String && c.String() != "" {
			parts = append(parts, c.String())
		}
	}
	if len(parts) == 0 {
		if t := gjson.GetBytes(resp, "choices.0.message.content").String(); t != "" {
			parts = append(parts, t)
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		return "", fmt.Errorf("no summary text in upstream response")
	}
	return text, nil
}

func usageOf(resp []byte) (in, out, total int64) {
	in = gjson.GetBytes(resp, "usage.input_tokens").Int()
	if in == 0 {
		in = gjson.GetBytes(resp, "usage.prompt_tokens").Int()
	}
	out = gjson.GetBytes(resp, "usage.output_tokens").Int()
	if out == 0 {
		out = gjson.GetBytes(resp, "usage.completion_tokens").Int()
	}
	total = gjson.GetBytes(resp, "usage.total_tokens").Int()
	if total == 0 {
		total = in + out
	}
	return
}

func runCompaction(exec rpcExecReq) (compactResult, error) {
	src := exec.Payload
	if !hasCompactionTrigger(src) && hasCompactionTrigger(exec.OriginalRequest) {
		src = exec.OriginalRequest
	}
	if len(src) == 0 {
		src = exec.OriginalRequest
	}
	model := strings.TrimSpace(exec.Model)
	if model == "" {
		model = strings.TrimSpace(gjson.GetBytes(src, "model").String())
	}
	body := prepareSummaryBody(src, model)
	resp, _, err := hostModelExecute(exec.HostCallbackID, model, body)
	if err != nil {
		return compactResult{}, err
	}
	summary, err := extractSummary(resp)
	if err != nil {
		return compactResult{}, err
	}
	capsule, err := sealCapsule(summary, model)
	if err != nil {
		return compactResult{}, err
	}
	in, out, total := usageOf(resp)
	return compactResult{
		JSON: buildCompactionJSON(model, capsule, in, out, total),
		SSE:  buildCompactionSSE(model, capsule, in, out, total),
	}, nil
}

func compactionItem(capsule, itemID, status string) string {
	item := `{"type":"compaction"}`
	item, _ = sjson.Set(item, "id", itemID)
	item, _ = sjson.Set(item, "status", status)
	item, _ = sjson.Set(item, "encrypted_content", capsule)
	return item
}

func usageNode(in, out, total int64) string {
	u := `{"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}`
	u, _ = sjson.Set(u, "input_tokens", in)
	u, _ = sjson.Set(u, "output_tokens", out)
	u, _ = sjson.Set(u, "total_tokens", total)
	return u
}

func buildCompactionJSON(model, capsule string, in, out, total int64) []byte {
	now := time.Now()
	r := `{"object":"response.compaction","status":"completed"}`
	r, _ = sjson.Set(r, "id", fmt.Sprintf("resp_clc_%d", now.UnixNano()))
	r, _ = sjson.Set(r, "created_at", now.Unix())
	r, _ = sjson.Set(r, "model", model)
	r, _ = sjson.SetRaw(r, "output", "["+compactionItem(capsule, fmt.Sprintf("cmp_clc_%d", now.UnixNano()), "completed")+"]")
	r, _ = sjson.SetRaw(r, "usage", usageNode(in, out, total))
	return []byte(r)
}

func sse(event string, data string) []byte {
	return []byte("event: " + event + "\ndata: " + data + "\n\n")
}

func buildCompactionSSE(model, capsule string, in, out, total int64) [][]byte {
	now := time.Now()
	rid := fmt.Sprintf("resp_clc_%d", now.UnixNano())
	iid := fmt.Sprintf("cmp_clc_%d", now.UnixNano())
	created := `{"object":"response","status":"in_progress","background":false,"error":null,"output":[]}`
	created, _ = sjson.Set(created, "id", rid)
	created, _ = sjson.Set(created, "created_at", now.Unix())
	created, _ = sjson.Set(created, "model", model)
	completed := `{"object":"response","status":"completed","background":false,"error":null}`
	completed, _ = sjson.Set(completed, "id", rid)
	completed, _ = sjson.Set(completed, "created_at", now.Unix())
	completed, _ = sjson.Set(completed, "completed_at", now.Unix())
	completed, _ = sjson.Set(completed, "model", model)
	completed, _ = sjson.SetRaw(completed, "output", "["+compactionItem(capsule, iid, "completed")+"]")
	completed, _ = sjson.SetRaw(completed, "usage", usageNode(in, out, total))

	ev := func(typ string, seq int, extra map[string]string) string {
		d := `{}`
		d, _ = sjson.Set(d, "type", typ)
		d, _ = sjson.Set(d, "sequence_number", seq)
		for k, v := range extra {
			d, _ = sjson.SetRaw(d, k, v)
		}
		return d
	}
	return [][]byte{
		sse("response.created", ev("response.created", 0, map[string]string{"response": created})),
		sse("response.in_progress", ev("response.in_progress", 1, map[string]string{"response": created})),
		sse("response.output_item.added", ev("response.output_item.added", 2, map[string]string{"output_index": "0", "item": compactionItem(capsule, iid, "in_progress")})),
		sse("response.output_item.done", ev("response.output_item.done", 3, map[string]string{"output_index": "0", "item": compactionItem(capsule, iid, "completed")})),
		sse("response.completed", ev("response.completed", 4, map[string]string{"response": completed})),
	}
}

type rpcExecReq struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func executeCompact(raw []byte) ([]byte, error) {
	var req rpcExecReq
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	res, err := runCompaction(req)
	if err != nil {
		return errorEnvelope("compact_error", err.Error()), nil
	}
	if req.Stream {
		var all []byte
		for _, c := range res.SSE {
			all = append(all, c...)
		}
		return okEnvelope(pluginapi.ExecutorResponse{Payload: all, Headers: http.Header{"Content-Type": []string{"text/event-stream"}}})
	}
	return okEnvelope(pluginapi.ExecutorResponse{Payload: res.JSON, Headers: http.Header{"Content-Type": []string{"application/json"}}})
}

func executeCompactStream(raw []byte) ([]byte, error) {
	var req rpcExecReq
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	streamID := strings.TrimSpace(req.StreamID)
	if streamID == "" {
		return errorEnvelope("executor_error", "stream_id is required for executor.execute_stream"), nil
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				closeStream(streamID, fmt.Sprintf("compaction panic: %v", r))
			}
		}()
		res, err := runCompaction(req)
		if err != nil {
			closeStream(streamID, err.Error())
			return
		}
		for _, c := range res.SSE {
			if err := emitStreamChunk(streamID, c); err != nil {
				closeStream(streamID, err.Error())
				return
			}
		}
		closeStream(streamID, "")
	}()
	return okEnvelope(map[string]any{"headers": http.Header{"Content-Type": []string{"text/event-stream"}}})
}
