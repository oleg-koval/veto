package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/oleg-koval/veto/internal/adapter/jev"
	"github.com/oleg-koval/veto/internal/adapter/shadowhistory"
	"github.com/oleg-koval/veto/internal/eval/paired"
	"github.com/oleg-koval/veto/pkg/router"
)

const (
	envJevShadowEnabled  = "VETO_EXPERIMENTAL_JEV_SHADOW"
	envTypeSafeAPIKey    = "TYPESAFE_API_KEY"
	envTypeSafeModel     = "TYPESAFE_MODEL"
	envJevShadowTimeout  = "VETO_JEV_SHADOW_TIMEOUT"
	envJevShadowEvidence = "VETO_JEV_SHADOW_EVIDENCE"
	envPrivateCapture    = "VETO_EXPERIMENTAL_PRIVATE_CAPTURE"
	envPrivateCaptureMax = "VETO_PRIVATE_CAPTURE_MAX"
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

type privateCaptureConfig struct {
	enabled  bool
	maxFiles int
	warnings []string
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
	capture := loadPrivateCaptureConfig(lookup)
	for _, warning := range config.warnings {
		fmt.Fprintln(warnings, "warning:", warning)
	}
	for _, warning := range capture.warnings {
		fmt.Fprintln(warnings, "warning:", warning)
	}
	var captureRecorder *paired.FileCaptureRecorder
	if capture.enabled {
		dir, err := ensurePrivateCaptureDirectory()
		if err == nil {
			captureRecorder, err = paired.NewFileCaptureRecorder(dir, capture.maxFiles)
		}
		if err != nil {
			fmt.Fprintln(warnings, "warning: private paired capture unavailable:", err)
			capture.enabled = false
		}
	}
	if !config.enabled && !capture.enabled {
		return
	}
	if config.evidencePath == "" {
		config.evidencePath = defaultShadowEvidencePath()
	}
	recorder := shadowhistory.NewFileRecorder(config.evidencePath, shadowhistory.DefaultMaxEvents)
	var decider router.ShadowDecider
	if !config.enabled {
		// Private capture can collect local route comparisons without any Jev
		// client, credential lookup, or network request.
	} else if config.apiKey == "" {
		fmt.Fprintln(warnings, "warning: Jev shadow is enabled but TYPESAFE_API_KEY is not set; sequential routing remains authoritative")
	} else {
		client, err := jev.New(jev.DefaultBaseURL, config.apiKey, config.model, httpClient)
		if err != nil {
			fmt.Fprintln(warnings, "warning: Jev shadow configuration is unavailable; sequential routing remains authoritative")
		} else {
			decider = client
		}
	}
	strategy := "jev:" + config.model
	if !config.enabled {
		strategy = "private-capture-only"
	}
	mgr.EnableDecisionShadow(decider, recorder, config.timeout, strategy)
	if captureRecorder != nil {
		mgr.SetPrivateRouteCaptureRecorder(captureRecorder)
		fmt.Fprintf(warnings, "warning: opt-in private paired capture is active; eligible task text and route-time model/tool snapshots are stored locally (retention: %d manifests)\n", capture.maxFiles)
	}
}

func loadPrivateCaptureConfig(lookup envLookup) privateCaptureConfig {
	config := privateCaptureConfig{maxFiles: paired.DefaultCaptureLimit}
	raw, exists := lookup(envPrivateCapture)
	if !exists || strings.TrimSpace(raw) == "" {
		return config
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		config.enabled = true
	case "0", "false", "no", "off":
		return config
	default:
		config.warnings = append(config.warnings, "VETO_EXPERIMENTAL_PRIVATE_CAPTURE must be 1/true or 0/false; private capture remains disabled")
		return config
	}
	if rawMax, ok := lookup(envPrivateCaptureMax); ok && strings.TrimSpace(rawMax) != "" {
		max, err := strconv.Atoi(strings.TrimSpace(rawMax))
		if err != nil || max < 1 || max > 500 {
			config.enabled = false
			config.warnings = append(config.warnings, "VETO_PRIVATE_CAPTURE_MAX must be an integer from 1 to 500; private capture remains disabled")
		} else {
			config.maxFiles = max
		}
	}
	return config
}

func ensurePrivateCaptureDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("cannot resolve home directory")
	}
	stateDir := filepath.Join(home, ".veto")
	if info, err := os.Lstat(stateDir); errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(stateDir, 0700); err != nil {
			return "", fmt.Errorf("create private state directory")
		}
	} else if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("~/.veto must be a real directory with private permissions")
	}
	dir := filepath.Join(stateDir, "paired-captures")
	if info, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(dir, 0700); err != nil {
			return "", fmt.Errorf("create paired capture directory")
		}
	} else if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("paired capture directory must be real and private")
	}
	return dir, nil
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
