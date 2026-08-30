package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	opencodert "github.com/oleg-koval/veto/pkg/opencode"
	"github.com/oleg-koval/veto/pkg/openroutercatalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveBuildVersionUsesGoModuleVersionForGoInstall(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/oleg-koval/veto", Version: "v0.1.0"}}
	assert.Equal(t, "0.1.0", resolveBuildVersion("dev", func() (*debug.BuildInfo, bool) { return info, true }))
	assert.Equal(t, "0.2.0", resolveBuildVersion("0.2.0", func() (*debug.BuildInfo, bool) { return info, true }))
	assert.Equal(t, "dev", resolveBuildVersion("dev", func() (*debug.BuildInfo, bool) { return nil, false }))
}

func TestDoctorOfflineHealthyState(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".veto")
	require.NoError(t, os.Mkdir(root, 0700))
	for _, dir := range []string{"skills", "plans", "checkpoints", "logs", "cache"} {
		require.NoError(t, os.Mkdir(filepath.Join(root, dir), 0700))
	}
	writeDoctorTestFile(t, filepath.Join(root, "credentials.json"), `{"CLAUDE_SUBSCRIPTION":"false"}`)
	writeDoctorTestFile(t, filepath.Join(root, "config.json"), `{}`)
	writeDoctorTestFile(t, filepath.Join(root, "models.json"), `[]`)
	writeDoctorTestFile(t, filepath.Join(root, "history.json"), `{}`)

	executable := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	deps := newDoctorTestDeps(home, executable)
	report := runDoctor(doctorOptions{offline: true}, deps)

	assert.True(t, report.OK, report.Checks)
	assert.Zero(t, report.Summary.Fail)
	assertDoctorCheck(t, report, "install.executable", doctorPass)
	assertDoctorCheck(t, report, "state.permissions", doctorPass)
	assertDoctorCheck(t, report, "state.json", doctorPass)
	assertDoctorCheck(t, report, "state.openrouter_catalog", doctorPass)
	assertDoctorCheck(t, report, "state.local_models", doctorPass)
	assertDoctorCheck(t, report, "state.skill_approvals", doctorPass)
	assertDoctorCheck(t, report, "dependencies.cli", doctorPass)
	assertDoctorCheck(t, report, "release.integrity", doctorWarn)
}

func TestDoctorValidatesOpenRouterCatalogCache(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".veto")
	cacheDir := filepath.Join(root, "cache")
	require.NoError(t, os.MkdirAll(cacheDir, 0700))
	executable := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	cachePath := filepath.Join(cacheDir, "openrouter-models.json")

	valid := map[string]any{
		"version":    1,
		"fetched_at": time.Now().UTC(),
		"models": []map[string]any{{
			"id": "openai/test", "name": "Test", "status": "available",
			"input_modalities": []string{"text"}, "output_modalities": []string{"text"},
			"supported_parameters": []string{},
		}},
	}
	body, err := json.Marshal(valid)
	require.NoError(t, err)
	writeDoctorTestFile(t, cachePath, string(body))

	healthy := runDoctor(doctorOptions{offline: true}, newDoctorTestDeps(home, executable))
	assertDoctorCheck(t, healthy, "state.openrouter_catalog", doctorPass)

	writeDoctorTestFile(t, cachePath, `{"version":1,"models":[]}`)
	broken := runDoctor(doctorOptions{offline: true}, newDoctorTestDeps(home, executable))
	assert.False(t, broken.OK)
	assertDoctorCheck(t, broken, "state.openrouter_catalog", doctorFail)
}

func TestDoctorWarnsForStaleOpenRouterCatalogCache(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".veto")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cache"), 0700))
	cachePath := filepath.Join(root, "cache", "openrouter-models.json")
	writeDoctorTestFile(t, cachePath, `{}`)
	executable := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	deps := newDoctorTestDeps(home, executable)
	deps.inspectOpenRouterCatalog = func(path string) (openroutercatalog.Snapshot, error) {
		assert.Equal(t, cachePath, path)
		return openroutercatalog.Snapshot{
			Models: []openroutercatalog.Model{{ID: "model"}}, State: openroutercatalog.StateStale, Offline: true,
		}, nil
	}

	report := runDoctor(doctorOptions{offline: true}, deps)
	assert.True(t, report.OK, report.Checks)
	assertDoctorCheck(t, report, "state.openrouter_catalog", doctorWarn)
}

func TestDoctorChecksConfiguredOpenCodeRuntime(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".veto")
	require.NoError(t, os.Mkdir(root, 0700))
	writeDoctorTestFile(t, filepath.Join(root, "config.json"), `{"opencode":{"mode":"cli"}}`)
	executable := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	deps := newDoctorTestDeps(home, executable)
	deps.inspectOpenCode = func(config opencodert.Config) (opencodert.Discovery, error) {
		assert.Equal(t, opencodert.ModeCLI, config.Mode)
		return opencodert.Discovery{Version: "1.18.5"}, nil
	}

	report := runDoctor(doctorOptions{offline: true}, deps)
	assert.True(t, report.OK, report.Checks)
	assertDoctorCheck(t, report, "runtime.opencode", doctorPass)

	deps.inspectOpenCode = func(opencodert.Config) (opencodert.Discovery, error) {
		return opencodert.Discovery{}, &opencodert.IncompatibleError{Version: "0.1.0", Detail: "missing provider API"}
	}
	report = runDoctor(doctorOptions{offline: true}, deps)
	assert.False(t, report.OK)
	assertDoctorCheck(t, report, "runtime.opencode", doctorFail)
}

func TestDoctorWarnsWhenOpenCodeCLIIsDetectedButUnconfigured(t *testing.T) {
	home := t.TempDir()
	executable := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	deps := newDoctorTestDeps(home, executable)
	deps.lookPath = func(name string) (string, error) {
		assert.Equal(t, "opencode", name)
		return `C:\Program Files\OpenCode\opencode.exe`, nil
	}
	report := runDoctor(doctorOptions{offline: true}, deps)
	assert.True(t, report.OK, report.Checks)
	assertDoctorCheck(t, report, "runtime.opencode", doctorWarn)
}

func TestDoctorFixesSafePermissionsWithoutRewritingJSON(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".veto")
	require.NoError(t, os.Mkdir(root, os.ModeSticky|0755))
	configPath := filepath.Join(root, "config.json")
	writeDoctorTestFile(t, configPath, `{}`)
	require.NoError(t, os.Chmod(configPath, 0644))
	executable := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	deps := newDoctorTestDeps(home, executable)

	before := runDoctor(doctorOptions{offline: true}, deps)
	assert.False(t, before.OK)
	assertDoctorCheck(t, before, "state.permissions", doctorFail)

	after := runDoctor(doctorOptions{offline: true, fix: true}, deps)
	assert.True(t, after.OK, after.Checks)
	assertDoctorCheck(t, after, "state.permissions", doctorFixed)
	rootInfo, err := os.Stat(root)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), rootInfo.Mode().Perm())
	fileInfo, err := os.Stat(configPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), fileInfo.Mode().Perm())
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, `{}`, string(data))
}

func TestDoctorRejectsMalformedJSONAndStateSymlinks(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".veto")
	require.NoError(t, os.Mkdir(root, 0700))
	writeDoctorTestFile(t, filepath.Join(root, "config.json"), `{broken`)
	target := filepath.Join(t.TempDir(), "credentials.json")
	writeDoctorTestFile(t, target, `{}`)
	require.NoError(t, os.Symlink(target, filepath.Join(root, "credentials.json")))
	executable := writeDoctorTestExecutable(t, t.TempDir(), "veto")

	report := runDoctor(doctorOptions{offline: true, fix: true}, newDoctorTestDeps(home, executable))
	assert.False(t, report.OK)
	assertDoctorCheck(t, report, "state.shape", doctorFail)
	assertDoctorCheck(t, report, "state.json", doctorFail)
	data, err := os.ReadFile(filepath.Join(root, "config.json"))
	require.NoError(t, err)
	assert.Equal(t, `{broken`, string(data))
}

func TestDoctorValidatesModelsSkillsDependenciesAndPathDuplicates(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".veto")
	require.NoError(t, os.Mkdir(root, 0700))
	writeDoctorTestFile(t, filepath.Join(root, "credentials.json"), `{"CLAUDE_SUBSCRIPTION":"true","ANTHROPIC_API_KEY":"must-not-leak"}`)
	writeDoctorTestFile(t, filepath.Join(root, "models.json"), `[{"name":"ollama-test","endpoint":"not a URL","model":""}]`)
	writeDoctorTestFile(t, filepath.Join(root, "config.json"), `{"skills":{"approved_files":["relative.md"],"approved_dirs":["/missing/skills"]}}`)

	firstDir := t.TempDir()
	secondDir := t.TempDir()
	executable := writeDoctorTestExecutable(t, firstDir, "veto")
	_ = writeDoctorTestExecutable(t, secondDir, "veto")
	deps := newDoctorTestDeps(home, executable)
	deps.pathEnv = func() string { return firstDir + string(os.PathListSeparator) + secondDir }
	deps.lookPath = func(string) (string, error) { return "", os.ErrNotExist }

	report := runDoctor(doctorOptions{offline: true}, deps)
	assert.False(t, report.OK)
	assertDoctorCheck(t, report, "install.path", doctorWarn)
	assertDoctorCheck(t, report, "state.local_models", doctorFail)
	assertDoctorCheck(t, report, "state.skill_approvals", doctorFail)
	assertDoctorCheck(t, report, "dependencies.cli", doctorFail)

	encoded, err := json.Marshal(report)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "must-not-leak")
}

func TestDoctorJSONOutputHasStableSchema(t *testing.T) {
	report := doctorReport{
		Checks:  []doctorCheck{{ID: "build.version", Status: doctorWarn, Message: "source build", Repairable: false}},
		OK:      true,
		Summary: doctorSummary{Warn: 1},
	}
	var out bytes.Buffer
	require.NoError(t, writeDoctorReport(&out, report, true))

	var decoded struct {
		Checks []struct {
			ID         string `json:"id"`
			Status     string `json:"status"`
			Message    string `json:"message"`
			Repairable bool   `json:"repairable"`
		} `json:"checks"`
		OK      bool          `json:"ok"`
		Summary doctorSummary `json:"summary"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	require.Len(t, decoded.Checks, 1)
	assert.Equal(t, "build.version", decoded.Checks[0].ID)
	assert.Equal(t, "WARN", decoded.Checks[0].Status)
	assert.True(t, decoded.OK)
	assert.Equal(t, 1, decoded.Summary.Warn)
}

func TestDoctorHumanOutputUsesResultLabels(t *testing.T) {
	report := doctorReport{Checks: []doctorCheck{
		{ID: "a", Status: doctorPass, Message: "ok"},
		{ID: "b", Status: doctorWarn, Message: "warning"},
		{ID: "c", Status: doctorFail, Message: "failure", Repairable: true},
		{ID: "d", Status: doctorFixed, Message: "repaired"},
	}}
	var out bytes.Buffer
	require.NoError(t, writeDoctorReport(&out, report, false))
	for _, label := range []string{"PASS", "WARN", "FAIL", "FIXED"} {
		assert.Contains(t, out.String(), label)
	}
}

func TestDoctorHelpExitsSuccessfully(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runDoctorCommand([]string{"--help"}, &stdout, &stderr, defaultDoctorDeps())
	assert.Zero(t, code)
	assert.Contains(t, stderr.String(), "-offline")
}

func assertDoctorCheck(t *testing.T, report doctorReport, id string, status doctorStatus) {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			assert.Equal(t, status, check.Status, "check %s: %s", id, check.Message)
			return
		}
	}
	t.Fatalf("doctor check %q not found: %+v", id, report.Checks)
}

func newDoctorTestDeps(home, executable string) doctorDeps {
	deps := defaultDoctorDeps()
	deps.userHome = func() (string, error) { return home, nil }
	deps.executable = func() (string, error) { return executable, nil }
	deps.pathEnv = func() string { return filepath.Dir(executable) }
	deps.getenv = func(string) string { return "" }
	deps.linkerVersion = "dev"
	deps.buildProvenance = "source"
	deps.readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	return deps
}

func writeDoctorTestFile(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(contents), 0600))
}

func writeDoctorTestExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("test binary"), 0755))
	return path
}

func TestDoctorMessagesDoNotContainCredentialKeysOrValues(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".veto")
	require.NoError(t, os.Mkdir(root, 0700))
	writeDoctorTestFile(t, filepath.Join(root, "credentials.json"), `{"OPENAI_API_KEY":"sk-secret-value"}`)
	executable := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	report := runDoctor(doctorOptions{offline: true}, newDoctorTestDeps(home, executable))
	var messages []string
	for _, check := range report.Checks {
		messages = append(messages, check.Message)
	}
	joined := strings.Join(messages, " ")
	assert.NotContains(t, joined, "sk-secret-value")
	assert.NotContains(t, joined, "OPENAI_API_KEY")
}

func TestDoctorChecksEnvironmentConfiguredCLIDependencies(t *testing.T) {
	home := t.TempDir()
	executable := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	deps := newDoctorTestDeps(home, executable)
	deps.getenv = func(name string) string {
		if name == "CLAUDE_SUBSCRIPTION" {
			return "true"
		}
		return ""
	}
	deps.lookPath = func(string) (string, error) { return "", os.ErrNotExist }

	report := runDoctor(doctorOptions{offline: true}, deps)
	assert.False(t, report.OK)
	assertDoctorCheck(t, report, "dependencies.cli", doctorFail)
}

func TestDoctorOnlyRequiresOllamaForLocalOllamaEndpoints(t *testing.T) {
	assert.True(t, doctorModelNeedsOllama(LocalModel{Name: "custom", Endpoint: "http://localhost:11434/v1/chat/completions"}))
	assert.True(t, doctorModelNeedsOllama(LocalModel{Name: "ollama-custom", Endpoint: "https://models.example/v1"}))
	assert.False(t, doctorModelNeedsOllama(LocalModel{Name: "custom", Endpoint: "https://models.example:11434/v1"}))
}

func TestDoctorProcess(t *testing.T) {
	if rawArgs := os.Getenv("VETO_TEST_DOCTOR_ARGS"); rawArgs != "" {
		os.Args = append([]string{"veto", "doctor"}, strings.Fields(rawArgs)...)
		main()
		return
	}

	t.Run("json success has no pending-skill notice", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0700))
		writeDoctorTestFile(t, filepath.Join(home, ".claude", "skills", "pending.md"), "---\nname: pending\n---\nbody")
		output, err := runDoctorTestProcess(home, "--json --offline")
		require.NoError(t, err, string(output))
		var report doctorReport
		require.NoError(t, json.NewDecoder(bytes.NewReader(output)).Decode(&report), string(output))
		assert.True(t, report.OK)
		assert.NotContains(t, string(output), "notice:")
	})

	t.Run("human failure exits one", func(t *testing.T) {
		home := t.TempDir()
		root := filepath.Join(home, ".veto")
		require.NoError(t, os.Mkdir(root, 0700))
		writeDoctorTestFile(t, filepath.Join(root, "config.json"), "{broken")
		output, err := runDoctorTestProcess(home, "--offline")
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr, string(output))
		assert.Equal(t, 1, exitErr.ExitCode())
		assert.Contains(t, string(output), "FAIL")
	})
}

func runDoctorTestProcess(home, args string) ([]byte, error) {
	command := exec.Command(os.Args[0], "-test.run=^TestDoctorProcess$")
	command.Env = cleanRootHelpTestEnv(os.Environ())
	command.Env = append(command.Env, "HOME="+home, "VETO_TEST_DOCTOR_ARGS="+args)
	return command.CombinedOutput()
}
