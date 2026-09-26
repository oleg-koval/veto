package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oleg-koval/veto/internal/adapter/jev"
	"github.com/oleg-koval/veto/internal/adapter/shadowhistory"
	"github.com/oleg-koval/veto/pkg/router"
)

const (
	envJevShadowEnabled  = "VETO_EXPERIMENTAL_JEV_SHADOW"
	envTypeSafeAPIKey    = "TYPESAFE_API_KEY"
	envTypeSafeModel     = "TYPESAFE_MODEL"
	envJevShadowTimeout  = "VETO_JEV_SHADOW_TIMEOUT"
	envJevShadowEvidence = "VETO_JEV_SHADOW_EVIDENCE"
)

type envLookup func(string) (string, bool)

type decisionShadowConfig struct {
	enabled      bool
	apiKey       string
	model        string
	timeout      time.Duration
	evidencePath string
	warnings     []string
}

// newRoutingManager builds a manager with saved candidate preferences and optional shadow evaluation.
func newRoutingManager(registry *router.Registry, gate *router.AdmissionGate, store router.Store) *router.Manager {
	mgr := router.NewManager(registry, gate, store)
	mgr.SetCandidatePreferences(loadCandidatePreferences())
	configureExperimentalDecisionShadow(mgr, os.Stderr, os.LookupEnv, http.DefaultClient)
	return mgr
}

// configureExperimentalDecisionShadow attaches the opt-in shadow observer and emits configuration warnings.
func configureExperimentalDecisionShadow(mgr *router.Manager, warnings io.Writer, lookup envLookup, httpClient *http.Client) {
	config := loadDecisionShadowConfig(lookup)
	if !config.enabled {
		for _, warning := range config.warnings {
			fmt.Fprintln(warnings, "warning:", warning)
		}
		return
	}
	for _, warning := range config.warnings {
		fmt.Fprintln(warnings, "warning:", warning)
	}
	recorder := shadowhistory.NewFileRecorder(config.evidencePath, shadowhistory.DefaultMaxEvents)
	var decider router.ShadowDecider
	if config.apiKey == "" {
		fmt.Fprintln(warnings, "warning: Jev shadow is enabled but TYPESAFE_API_KEY is not set; sequential routing remains authoritative")
	} else {
		client, err := jev.New(jev.DefaultBaseURL, config.apiKey, config.model, httpClient)
		if err != nil {
			fmt.Fprintln(warnings, "warning: Jev shadow configuration is unavailable; sequential routing remains authoritative")
		} else {
			decider = client
		}
	}
	mgr.EnableDecisionShadow(decider, recorder, config.timeout, "jev:"+config.model)
}

// loadDecisionShadowConfig reads shadow prerequisites only after the enable switch opts in.
func loadDecisionShadowConfig(lookup envLookup) decisionShadowConfig {
	raw, exists := lookup(envJevShadowEnabled)
	if !exists || strings.TrimSpace(raw) == "" {
		return decisionShadowConfig{}
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
	case "0", "false", "no", "off":
		return decisionShadowConfig{}
	default:
		return decisionShadowConfig{warnings: []string{"VETO_EXPERIMENTAL_JEV_SHADOW must be 1/true or 0/false; shadow remains disabled"}}
	}

	config := decisionShadowConfig{enabled: true, model: jev.DefaultModel, timeout: time.Second}
	config.apiKey, _ = lookup(envTypeSafeAPIKey)
	if model, ok := lookup(envTypeSafeModel); ok && strings.TrimSpace(model) != "" {
		config.model = strings.TrimSpace(model)
	}
	if rawTimeout, ok := lookup(envJevShadowTimeout); ok && strings.TrimSpace(rawTimeout) != "" {
		parsed, err := time.ParseDuration(rawTimeout)
		if err != nil || parsed <= 0 {
			config.warnings = append(config.warnings, "VETO_JEV_SHADOW_TIMEOUT is invalid; using 1s")
		} else {
			config.timeout = parsed
		}
	}
	if path, ok := lookup(envJevShadowEvidence); ok && strings.TrimSpace(path) != "" {
		config.evidencePath = filepath.Clean(path)
	} else {
		config.evidencePath = defaultShadowEvidencePath()
	}
	return config
}

// defaultShadowEvidencePath returns the shadow JSONL path under the current user's .veto directory.
func defaultShadowEvidencePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".veto", "jev-shadow-v1.jsonl")
}
