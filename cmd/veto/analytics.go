package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	analyticsPolicyVersion = 1
	analyticsOptIn         = "opt_in"
	analyticsOptOut        = "opt_out"
)

type analyticsConfig struct {
	RemoteSharing string `json:"remote_sharing,omitempty"`
	PolicyVersion int    `json:"policy_version,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

type analyticsStatus struct {
	LocalCollection       bool   `json:"local_collection"`
	LocalPath             string `json:"local_path"`
	LocalRetentionDays    int    `json:"local_retention_days"`
	RemoteCollection      string `json:"remote_collection"`
	RemoteSharing         string `json:"remote_sharing"`
	PolicyVersion         int    `json:"policy_version"`
	RemoteTransportActive bool   `json:"remote_transport_active"`
}

func cmdAnalytics(args []string) {
	if code := runAnalyticsCommand(args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func runAnalyticsCommand(args []string, output, diagnostics io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printAnalyticsUsage(output)
		return 0
	}

	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("analytics status", flag.ContinueOnError)
		fs.SetOutput(diagnostics)
		jsonOutput := fs.Bool("json", false, "emit one machine-readable status object")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			fmt.Fprintln(diagnostics, "analytics status accepts no positional arguments")
			return 2
		}
		status, err := currentAnalyticsStatus()
		if err != nil {
			fmt.Fprintln(diagnostics, "analytics status failed:", err)
			return 1
		}
		if *jsonOutput {
			if err := json.NewEncoder(output).Encode(status); err != nil {
				fmt.Fprintln(diagnostics, "analytics status failed:", err)
				return 1
			}
			return 0
		}
		printAnalyticsStatus(output, status)
		return 0
	case "enable", "disable":
		fs := flag.NewFlagSet("analytics "+args[0], flag.ContinueOnError)
		fs.SetOutput(diagnostics)
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			fmt.Fprintf(diagnostics, "analytics %s accepts no positional arguments\n", args[0])
			return 2
		}
		preference := analyticsOptOut
		if args[0] == "enable" {
			preference = analyticsOptIn
		}
		if err := saveAnalyticsPreference(preference); err != nil {
			fmt.Fprintln(diagnostics, "analytics preference failed:", err)
			return 1
		}
		if preference == analyticsOptIn {
			fmt.Fprintln(output, "  Future remote analytics preference: opt-in")
			fmt.Fprintln(output, "  Nothing is sent today; Veto has no remote analytics transport.")
		} else {
			fmt.Fprintln(output, "  Future remote analytics preference: opt-out")
			fmt.Fprintln(output, "  Local diagnostic logging remains available on this machine.")
		}
		return 0
	default:
		fmt.Fprintf(diagnostics, "unknown analytics command %q\n\n", args[0])
		printAnalyticsUsage(diagnostics)
		return 2
	}
}

func printAnalyticsUsage(w io.Writer) {
	fmt.Fprintln(w, "USAGE")
	fmt.Fprintln(w, "  veto analytics <status|enable|disable> [--json]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Local diagnostic events stay on this machine. Remote analytics are not implemented.")
	fmt.Fprintln(w, "Enable records an opt-in preference for a future reviewed export; it sends nothing today.")
}

func currentAnalyticsStatus() (analyticsStatus, error) {
	cfg, err := loadAnalyticsConfig()
	if err != nil {
		return analyticsStatus{}, err
	}
	return analyticsStatus{
		LocalCollection:       true,
		LocalPath:             "~/.veto/logs/veto-YYYY-MM-DD.log",
		LocalRetentionDays:    7,
		RemoteCollection:      "not_implemented",
		RemoteSharing:         analyticsPreferenceLabel(cfg.RemoteSharing),
		PolicyVersion:         analyticsPolicyVersion,
		RemoteTransportActive: false,
	}, nil
}

func printAnalyticsStatus(w io.Writer, status analyticsStatus) {
	fmt.Fprintln(w, "Veto analytics")
	fmt.Fprintf(w, "  Local diagnostic collection: %s\n", boolLabel(status.LocalCollection))
	fmt.Fprintf(w, "  Local ledger: %s (%d-day rolling retention)\n", status.LocalPath, status.LocalRetentionDays)
	fmt.Fprintln(w, "  Remote collection: not implemented; nothing leaves this machine")
	fmt.Fprintf(w, "  Future remote sharing preference: %s\n", status.RemoteSharing)
	fmt.Fprintln(w, "  Change preference: veto analytics enable|disable")
}

func boolLabel(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}

func loadAnalyticsConfig() (analyticsConfig, error) {
	data, err := os.ReadFile(vetoCfgPath())
	if errors.Is(err, os.ErrNotExist) {
		return analyticsConfig{}, nil
	}
	if err != nil {
		return analyticsConfig{}, err
	}
	var full map[string]json.RawMessage
	if err := json.Unmarshal(data, &full); err != nil || full == nil {
		return analyticsConfig{}, errors.New("config.json is malformed; refusing to guess or rewrite it")
	}
	raw, ok := full["analytics"]
	if !ok {
		return analyticsConfig{}, nil
	}
	var cfg analyticsConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return analyticsConfig{}, errors.New("config.json has an invalid analytics section")
	}
	if cfg.RemoteSharing != "" && cfg.RemoteSharing != analyticsOptIn && cfg.RemoteSharing != analyticsOptOut {
		return analyticsConfig{}, fmt.Errorf("config.json has an invalid analytics preference %q", cfg.RemoteSharing)
	}
	return cfg, nil
}

func saveAnalyticsPreference(preference string) error {
	if preference != analyticsOptIn && preference != analyticsOptOut {
		return fmt.Errorf("invalid analytics preference %q", preference)
	}
	return mutateVetoConfig(vetoCfgPath(), func(full map[string]json.RawMessage) error {
		cfg := analyticsConfig{
			RemoteSharing: preference,
			PolicyVersion: analyticsPolicyVersion,
			UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		full["analytics"] = raw
		return nil
	})
}

func analyticsPreferenceLabel(preference string) string {
	switch preference {
	case analyticsOptIn:
		return "opt-in"
	case analyticsOptOut:
		return "opt-out"
	default:
		return "not set"
	}
}

func remoteAnalyticsOptedIn() bool {
	cfg, err := loadAnalyticsConfig()
	return err == nil && strings.TrimSpace(cfg.RemoteSharing) == analyticsOptIn && cfg.PolicyVersion == analyticsPolicyVersion
}
