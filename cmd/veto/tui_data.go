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
	if auth := codexCLIAuthenticationContext(ctx); auth != codexAuthNone {
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
	plans := make([]controlplane.PlanSnapshot, 0, len(paths))
	for _, path := range paths {
		plans = append(plans, controlplane.PlanSnapshot{Name: filepath.Base(path)})
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
	result := make([]controlplane.HistorySnapshot, 0, 128)
	missions := readTUIMissions()
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
		for eventIndex := len(events) - 1; eventIndex >= 0; eventIndex-- {
			event := events[eventIndex]
			model, harness := tuiHistoryIdentity(event)
			snapshot := controlplane.HistorySnapshot{
				Timestamp: event.Timestamp, EventID: event.EventID, RunID: event.RunID, TaskID: event.TaskID,
				TaskKind: event.TaskKind, Risk: event.Risk, Type: string(event.Type), Model: model,
				Runtime: harness, Status: event.Status, Reasons: append([]string(nil), event.Reasons...), Detail: event.Detail,
			}
			if mission, ok := missions[event.RunID]; ok {
				snapshot.MissionTitle = mission.Title
				snapshot.Objective = mission.Objective
			}
			if event.Confidence != nil {
				snapshot.Confidence, snapshot.ConfidenceKnown = *event.Confidence, true
			}
			if event.EstimatedTokens != nil {
				snapshot.EstimatedTokens, snapshot.EstimatedTokensKnown = *event.EstimatedTokens, true
			}
			if event.EstimatedCostUSD != nil {
				snapshot.EstimatedCostUSD, snapshot.EstimatedCostKnown = *event.EstimatedCostUSD, true
			}
			if event.Usage != nil {
				snapshot.InputTokens = event.Usage.InputTokens
				snapshot.CachedInputTokens = event.Usage.CachedInputTokens
				// A positive value predates the explicit presence bit; keep those
				// records readable while preserving a reported zero going forward.
				snapshot.CachedInputKnown = event.Usage.CachedInputKnown || event.Usage.CachedInputTokens > 0
				snapshot.OutputTokens = event.Usage.OutputTokens
				snapshot.TotalTokens = event.Usage.TotalTokens
				snapshot.UsageKnown = true
			}
			if event.CostUSD != nil {
				snapshot.CostUSD, snapshot.CostKnown = *event.CostUSD, true
			}
			if event.LatencyMS != nil {
				snapshot.LatencyMS, snapshot.LatencyKnown = *event.LatencyMS, true
			}
			result = append(result, snapshot)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Timestamp.After(result[j].Timestamp) })
	return result
}

func tuiHistoryIdentity(event ledger.Event) (string, string) {
	if strings.EqualFold(event.Model, "codex") || event.Runtime == "codex-cli" {
		return "", "Codex CLI"
	}
	return event.Model, displayHarness(event.Runtime)
}

func displayHarness(runtime string) string {
	switch runtime {
	case "claude-cli":
		return "Claude CLI"
	case "openai-api":
		return "OpenAI API"
	case "openrouter-api":
		return "OpenRouter API"
	case "opencode":
		return "OpenCode"
	case "openai-compatible":
		return "Local API"
	default:
		return runtime
	}
}

func readTUIIntegrations() []controlplane.IntegrationSnapshot {
	integrations := make([]controlplane.IntegrationSnapshot, 0, 3)
	if config, configured, err := loadOpenCodeConfig(vetoCfgPath()); err == nil && configured {
		integrations = append(integrations, controlplane.IntegrationSnapshot{Name: "OpenCode", Status: "configured", Detail: "mode: " + string(config.Mode), PrimaryAction: "status"})
	} else {
		integrations = append(integrations, controlplane.IntegrationSnapshot{Name: "OpenCode", Status: "not configured", Detail: "Connect Veto to the installed OpenCode runtime", PrimaryAction: "connect"})
	}
	if home, err := hermesHome(""); err == nil {
		if state, statusErr := hermesintegration.Status(home); statusErr == nil {
			status := "current"
			action := "status"
			if state.Missing > 0 || state.Modified > 0 {
				status = "needs attention"
				action = "repair"
			}
			integrations = append(integrations, controlplane.IntegrationSnapshot{Name: "Hermes", Status: status, Detail: fmt.Sprintf("installed=%d missing=%d modified=%d", state.Installed, state.Missing, state.Modified), PrimaryAction: action})
		} else {
			integrations = append(integrations, controlplane.IntegrationSnapshot{Name: "Hermes", Status: "unavailable", Detail: strings.TrimSpace(statusErr.Error()), PrimaryAction: "install"})
		}
	}
	integrations = append(integrations, controlplane.IntegrationSnapshot{
		Name:          "Impeccable",
		Status:        "available",
		Detail:        "Install curated design skills directly into Veto",
		PrimaryAction: "install",
	})
	return integrations
}
