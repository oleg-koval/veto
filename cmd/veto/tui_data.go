package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	hermesintegration "github.com/oleg-koval/veto/integrations/hermes"
	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/pkg/ledger"
)

// loadTUISnapshot reads only bounded, redacted local metadata. It is a
// composition-root adapter so the application service remains independent of
// CLI-specific filesystem paths and doctor implementations.
func loadTUISnapshot(_ context.Context) (controlplane.Snapshot, error) {
	snapshot := controlplane.Snapshot{Status: "ready"}
	if status, err := currentAnalyticsStatus(); err == nil {
		snapshot.Analytics = controlplane.AnalyticsSnapshot{
			LocalCollection:       status.LocalCollection,
			LocalPath:             status.LocalPath,
			RetentionDays:         status.LocalRetentionDays,
			RemoteSharing:         status.RemoteSharing,
			RemoteTransportActive: status.RemoteTransportActive,
		}
	} else {
		snapshot.Health = append(snapshot.Health, controlplane.HealthSnapshot{ID: "analytics.config", Status: "WARN", Message: "analytics preference could not be read"})
	}

	report := runDoctor(doctorOptions{offline: true}, defaultDoctorDeps())
	for _, check := range report.Checks {
		snapshot.Health = append(snapshot.Health, controlplane.HealthSnapshot{ID: check.ID, Status: string(check.Status), Message: check.Message})
	}
	snapshot.History = readTUIHistory()
	snapshot.Plans = readTUIPlans()
	snapshot.Providers = readTUIProviders()
	snapshot.Integrations = readTUIIntegrations()
	return snapshot, nil
}

func readTUIProviders() []controlplane.ProviderSnapshot {
	creds, _ := loadCredentials()
	providers := make([]controlplane.ProviderSnapshot, 0, len(knownProviders)+2)
	for _, provider := range knownProviders {
		configured := os.Getenv(provider.envKey) != "" || creds[provider.envKey] != ""
		if provider.provider == "anthropic" && (os.Getenv("CLAUDE_SUBSCRIPTION") == "true" || creds["CLAUDE_SUBSCRIPTION"] == "true") {
			configured = true
		}
		providers = append(providers, controlplane.ProviderSnapshot{Name: provider.name, Configured: configured})
	}
	if auth := codexCLIAuthentication(); auth != codexAuthNone {
		providers = append(providers, controlplane.ProviderSnapshot{Name: "Codex", Configured: true})
	}
	if _, configured, err := loadOpenCodeConfig(vetoCfgPath()); err == nil && configured {
		providers = append(providers, controlplane.ProviderSnapshot{Name: "OpenCode", Configured: true})
	}
	return providers
}

func readTUIPlans() []controlplane.PlanSnapshot {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	paths, err := filepath.Glob(filepath.Join(home, ".veto", "plans", "*.md"))
	if err != nil {
		return nil
	}
	sort.Strings(paths)
	const maxPlans = 40
	plans := make([]controlplane.PlanSnapshot, 0, minInt(len(paths), maxPlans))
	for index := 0; index < len(paths) && index < maxPlans; index++ {
		plans = append(plans, controlplane.PlanSnapshot{Name: filepath.Base(paths[index])})
	}
	return plans
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func readTUIHistory() []controlplane.HistorySnapshot {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	paths, err := filepath.Glob(filepath.Join(home, ".veto", "logs", "veto-*.log"))
	if err != nil {
		return nil
	}
	sort.Strings(paths)
	const maxHistory = 40
	result := make([]controlplane.HistorySnapshot, 0, maxHistory)
	for index := len(paths) - 1; index >= 0 && len(result) < maxHistory; index-- {
		file, openErr := os.Open(paths[index])
		if openErr != nil {
			continue
		}
		events, _, readErr := ledger.Read(file)
		_ = file.Close()
		if readErr != nil {
			continue
		}
		for eventIndex := len(events) - 1; eventIndex >= 0 && len(result) < maxHistory; eventIndex-- {
			event := events[eventIndex]
			result = append(result, controlplane.HistorySnapshot{Timestamp: event.Timestamp, Type: string(event.Type), Model: event.Model, Runtime: event.Runtime, Status: event.Status})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Timestamp.After(result[j].Timestamp) })
	return result
}

func readTUIIntegrations() []controlplane.IntegrationSnapshot {
	integrations := make([]controlplane.IntegrationSnapshot, 0, 2)
	if config, configured, err := loadOpenCodeConfig(vetoCfgPath()); err == nil && configured {
		integrations = append(integrations, controlplane.IntegrationSnapshot{Name: "OpenCode", Status: "configured", Detail: string(config.Mode)})
	} else {
		integrations = append(integrations, controlplane.IntegrationSnapshot{Name: "OpenCode", Status: "not configured", Detail: "run veto opencode connect"})
	}
	if home, err := hermesHome(""); err == nil {
		if state, statusErr := hermesintegration.Status(home); statusErr == nil {
			status := "current"
			if state.Missing > 0 || state.Modified > 0 {
				status = "needs attention"
			}
			integrations = append(integrations, controlplane.IntegrationSnapshot{Name: "Hermes", Status: status, Detail: fmt.Sprintf("installed=%d missing=%d modified=%d", state.Installed, state.Missing, state.Modified)})
		} else {
			integrations = append(integrations, controlplane.IntegrationSnapshot{Name: "Hermes", Status: "unavailable", Detail: strings.TrimSpace(statusErr.Error())})
		}
	}
	return integrations
}
