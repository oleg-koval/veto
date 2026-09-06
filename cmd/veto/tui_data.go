package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	hermesintegration "github.com/oleg-koval/veto/integrations/hermes"
	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/pkg/dispatch"
	"github.com/oleg-koval/veto/pkg/ledger"
)

// loadTUISnapshot reads only bounded, redacted local metadata. It is a
// composition-root adapter so the application service remains independent of
// CLI-specific filesystem paths and doctor implementations.
var tuiDoctorCache struct {
	sync.Mutex
	report doctorReport
	at     time.Time
}

const tuiDoctorCacheTTL = 30 * time.Second

func loadTUISnapshot(ctx context.Context) (controlplane.Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
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
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}

	report := cachedTUIDoctor(ctx)
	for _, check := range report.Checks {
		snapshot.Health = append(snapshot.Health, controlplane.HealthSnapshot{ID: check.ID, Status: string(check.Status), Message: check.Message})
	}
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	snapshot.History = readTUIHistory()
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	snapshot.Plans = readTUIPlans()
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	snapshot.Providers = readTUIProviders(ctx)
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	snapshot.Integrations = readTUIIntegrations()
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func cachedTUIDoctor(ctx context.Context) doctorReport {
	now := time.Now()
	tuiDoctorCache.Lock()
	if !tuiDoctorCache.at.IsZero() && now.Sub(tuiDoctorCache.at) < tuiDoctorCacheTTL {
		report := tuiDoctorCache.report
		tuiDoctorCache.Unlock()
		return report
	}
	tuiDoctorCache.Unlock()
	if ctx.Err() != nil {
		return doctorReport{}
	}
	report := runDoctor(doctorOptions{ctx: ctx, offline: true}, defaultDoctorDeps())
	if ctx.Err() != nil {
		return report
	}
	tuiDoctorCache.Lock()
	tuiDoctorCache.report = report
	tuiDoctorCache.at = time.Now()
	tuiDoctorCache.Unlock()
	return report
}

func readTUIProviders(ctx context.Context) []controlplane.ProviderSnapshot {
	creds, _ := loadCredentials()
	providers := make([]controlplane.ProviderSnapshot, 0, len(knownProviders)+2)
	for _, native := range nativeAgentStatuses(ctx, dispatch.NewAvailabilityStore(availabilityPath())) {
		name := native.Name
		if len(name) > 0 {
			name = strings.ToUpper(name[:1]) + name[1:]
		}
		models := []string{"native default"}
		if native.Name == "claude" {
			models = []string{"haiku", "sonnet", "opus"}
		}
		providers = append(providers, controlplane.ProviderSnapshot{Name: name, Configured: native.Auth == dispatch.AuthAuthenticated, Installed: native.Installed, Auth: string(native.Auth), Billing: string(native.Billing), Unavailable: native.Unavailable, Warning: native.Warning, Models: models})
	}
	for _, provider := range knownProviders {
		configured := os.Getenv(provider.envKey) != "" || creds[provider.envKey] != ""
		if provider.provider == "anthropic" && (os.Getenv("CLAUDE_SUBSCRIPTION") == "true" || creds["CLAUDE_SUBSCRIPTION"] == "true") {
			configured = true
		}
		providers = append(providers, controlplane.ProviderSnapshot{Name: provider.name, Configured: configured})
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
	plans := make([]controlplane.PlanSnapshot, 0, min(len(paths), maxPlans))
	for index := 0; index < len(paths) && index < maxPlans; index++ {
		plans = append(plans, controlplane.PlanSnapshot{Name: filepath.Base(paths[index])})
	}
	return plans
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
	paths = append(paths, experimentPath())
	sort.Strings(paths)
	const maxHistory = 40
	result := make([]controlplane.HistorySnapshot, 0, len(paths)*maxHistory)
	for index := len(paths) - 1; index >= 0; index-- {
		file, openErr := os.Open(paths[index])
		if openErr != nil {
			continue
		}
		events, _, readErr := ledger.Read(file)
		_ = file.Close()
		if readErr != nil {
			continue
		}
		first := max(0, len(events)-maxHistory)
		for eventIndex := len(events) - 1; eventIndex >= first; eventIndex-- {
			event := events[eventIndex]
			result = append(result, controlplane.HistorySnapshot{Timestamp: event.Timestamp, Type: string(event.Type), Model: event.Model, Runtime: event.Runtime, Status: event.Status})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Timestamp.After(result[j].Timestamp) })
	if len(result) > maxHistory {
		result = result[:maxHistory]
	}
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
