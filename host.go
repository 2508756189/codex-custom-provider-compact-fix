package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type hostModelExecutionRequest struct {
	pluginapi.HostModelExecutionRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcStreamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
	Error    string `json:"error,omitempty"`
}

type rpcStreamCloseRequest struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error,omitempty"`
}

// hostModelExecute runs a non-stream model call through the host (summary hop).
func hostModelExecute(callbackID, model string, body []byte) ([]byte, int, error) {
	raw, err := callHost(pluginabi.MethodHostModelExecute, hostModelExecutionRequest{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
			EntryProtocol: "openai-response",
			ExitProtocol:  "openai-response",
			Model:         model,
			Stream:        false,
			Body:          body,
		},
		HostCallbackID: callbackID,
	})
	if err != nil {
		return nil, 0, err
	}
	var resp pluginapi.HostModelExecutionResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, 0, err
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(resp.Body))
		if len(msg) > 600 {
			msg = msg[:600]
		}
		return resp.Body, resp.StatusCode, fmt.Errorf("summary upstream status %d: %s", resp.StatusCode, msg)
	}
	return resp.Body, resp.StatusCode, nil
}

func emitStreamChunk(streamID string, payload []byte) error {
	_, err := callHost(pluginabi.MethodHostStreamEmit, rpcStreamEmitRequest{StreamID: streamID, Payload: payload})
	return err
}

func closeStream(streamID, errMsg string) {
	_, _ = callHost(pluginabi.MethodHostStreamClose, rpcStreamCloseRequest{StreamID: streamID, Error: strings.TrimSpace(errMsg)})
}
