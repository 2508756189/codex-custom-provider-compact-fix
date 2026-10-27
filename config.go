package main

import (
	"encoding/json"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const defaultReasoningPlaceholder = "[reasoning not retained for this turn]"

var defaultMatchSubstrings = []string{"deepseek"}

type pluginConfig struct {
	Enabled              bool     `yaml:"enabled" json:"enabled"`
	Models               []string `yaml:"models" json:"models"`
	MatchSubstrings      []string `yaml:"match_substrings" json:"match_substrings"`
	ReasoningPlaceholder string   `yaml:"reasoning_placeholder" json:"reasoning_placeholder"`
	SummaryPrompt        string   `yaml:"summary_prompt" json:"summary_prompt"`
	Priority             int      `yaml:"priority" json:"priority"`
	DebugLogFile         string   `yaml:"debug_log_file" json:"debug_log_file"`
}

var (
	cfgMu sync.RWMutex
	cfg   = defaultConfig()
)

func defaultConfig() pluginConfig {
	return pluginConfig{
		Enabled:              true,
		MatchSubstrings:      append([]string(nil), defaultMatchSubstrings...),
		ReasoningPlaceholder: defaultReasoningPlaceholder,
		Priority:             50,
	}
}

func configure(request []byte) error {
	var req lifecycleRequest
	if len(request) > 0 {
		if err := json.Unmarshal(request, &req); err != nil {
			return err
		}
	}
	next := defaultConfig()
	if len(req.ConfigYAML) > 0 {
		if err := yaml.Unmarshal(req.ConfigYAML, &next); err != nil {
			return err
		}
	}
	if strings.TrimSpace(next.ReasoningPlaceholder) == "" {
		next.ReasoningPlaceholder = defaultReasoningPlaceholder
	}
	if len(next.Models) == 0 && len(next.MatchSubstrings) == 0 {
		next.MatchSubstrings = append([]string(nil), defaultMatchSubstrings...)
	}
	cfgMu.Lock()
	cfg = next
	cfgMu.Unlock()
	return nil
}

func currentConfig() pluginConfig {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	out := cfg
	out.Models = append([]string(nil), cfg.Models...)
	out.MatchSubstrings = append([]string(nil), cfg.MatchSubstrings...)
	return out
}

func modelAllowed(model string) bool {
	c := currentConfig()
	if !c.Enabled {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		return false
	}
	for _, exact := range c.Models {
		if strings.EqualFold(strings.TrimSpace(exact), name) {
			return true
		}
	}
	if len(c.Models) > 0 {
		// Exact list present: substrings only apply when exact list is empty,
		// unless substrings are also configured as additional filters.
		// Spec: empty models => use match_substrings. Non-empty models => exact only.
		return false
	}
	subs := c.MatchSubstrings
	if len(subs) == 0 {
		subs = defaultMatchSubstrings
	}
	for _, sub := range subs {
		sub = strings.ToLower(strings.TrimSpace(sub))
		if sub != "" && strings.Contains(name, sub) {
			return true
		}
	}
	return false
}
