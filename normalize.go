package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// rewriteForAllowlisted applies capsule expansion and DeepSeek reasoning
// passback to Responses bodies for allowlisted models only.
func rewriteForAllowlisted(model string, body []byte) ([]byte, bool) {
	if len(body) == 0 || !modelAllowed(model) {
		return body, false
	}
	changed := false
	if hasCompactionItem(body) {
		var c bool
		body, c = expandCapsules(body)
		changed = changed || c
	}
	if hasCompactionTrigger(body) {
		// The trigger is consumed by the executor path; never forward it upstream.
		return body, changed
	}
	var c bool
	body, c = ensureReasoningPassback(body, currentConfig().ReasoningPlaceholder)
	return body, changed || c
}

func normalizeRequest(raw []byte) ([]byte, error) {
	var req pluginapi.RequestTransformRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	out, changed := rewriteForAllowlisted(req.Model, req.Body)
	debugf("normalize model=%s from=%s to=%s bodyLen=%d allowed=%v changed=%v", req.Model, req.FromFormat, req.ToFormat, len(req.Body), modelAllowed(req.Model), changed)
	return okEnvelope(pluginapi.PayloadResponse{Body: out})
}

func interceptRequest(raw []byte) ([]byte, error) {
	var req pluginapi.RequestInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	out, changed := rewriteForAllowlisted(req.Model, req.Body)
	debugf("intercept model=%s requested=%s src=%s to=%s bodyLen=%d allowed=%v changed=%v", req.Model, req.RequestedModel, req.SourceFormat, req.ToFormat, len(req.Body), modelAllowed(req.Model), changed)
	if !changed {
		return okEnvelope(pluginapi.RequestInterceptResponse{})
	}
	return okEnvelope(pluginapi.RequestInterceptResponse{Body: out})
}

// debugf appends a line to debug_log_file when configured. Never logs bodies.
func debugf(format string, args ...any) {
	path := currentConfig().DebugLogFile
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
