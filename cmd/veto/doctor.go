package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/oleg-koval/veto/pkg/router"
)

type doctorStatus string

const (
	doctorPass  doctorStatus = "PASS"
	doctorWarn  doctorStatus = "WARN"
	doctorFail  doctorStatus = "FAIL"
	doctorFixed doctorStatus = "FIXED"
)

const doctorStateFileMaxBytes = 16 << 20

type doctorCheck struct {
	ID         string       `json:"id"`
	Status     doctorStatus `json:"status"`
	Message    string       `json:"message"`
	Repairable bool         `json:"repairable"`
}

type doctorSummary struct {
	Pass  int `json:"pass"`
	Warn  int `json:"warn"`
	Fail  int `json:"fail"`
	Fixed int `json:"fixed"`
}

type doctorReport struct {
	Checks  []doctorCheck `json:"checks"`
	OK      bool          `json:"ok"`
	Summary doctorSummary `json:"summary"`
}

type doctorOptions struct {
	fix     bool
	offline bool
}

type doctorFilesystem interface {
	Lstat(string) (os.FileInfo, error)
	Stat(string) (os.FileInfo, error)
	ReadFile(string) ([]byte, error)
	ReadDir(string) ([]os.DirEntry, error)
	MkdirAll(string, os.FileMode) error
	Chmod(string, os.FileMode) error
	EvalSymlinks(string) (string, error)
	CreateTemp(string, string) (*os.File, error)
	Open(string) (*os.File, error)
	Rename(string, string) error
	Remove(string) error
}

type osDoctorFilesystem struct{}

func (osDoctorFilesystem) Lstat(path string) (os.FileInfo, error) { return os.Lstat(path) }
func (osDoctorFilesystem) Stat(path string) (os.FileInfo, error)  { return os.Stat(path) }
func (osDoctorFilesystem) ReadFile(path string) ([]byte, error)   { return os.ReadFile(path) }
func (osDoctorFilesystem) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}
func (osDoctorFilesystem) MkdirAll(path string, mode os.FileMode) error {
	return os.MkdirAll(path, mode)
}
func (osDoctorFilesystem) Chmod(path string, mode os.FileMode) error { return os.Chmod(path, mode) }
func (osDoctorFilesystem) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
func (osDoctorFilesystem) CreateTemp(dir, pattern string) (*os.File, error) {
	return os.CreateTemp(dir, pattern)
}
func (osDoctorFilesystem) Open(path string) (*os.File, error) { return os.Open(path) }
func (osDoctorFilesystem) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
func (osDoctorFilesystem) Remove(path string) error { return os.Remove(path) }

type doctorDeps struct {
	fs                doctorFilesystem
	userHome          func() (string, error)
	executable        func() (string, error)
	pathEnv           func() string
	lookPath          func(string) (string, error)
	readBuildInfo     func() (*debug.BuildInfo, bool)
	linkerVersion     string
	buildProvenance   string
	goos              string
	goarch            string
	httpDo            func(*http.Request) (*http.Response, error)
	validateCandidate func(string, string) error
}

func defaultDoctorDeps() doctorDeps {
	return doctorDeps{
		fs:                osDoctorFilesystem{},
		userHome:          os.UserHomeDir,
		executable:        os.Executable,
		pathEnv:           func() string { return os.Getenv("PATH") },
		lookPath:          exec.LookPath,
		readBuildInfo:     debug.ReadBuildInfo,
		linkerVersion:     version,
		buildProvenance:   buildProvenance,
		goos:              runtime.GOOS,
		goarch:            runtime.GOARCH,
		httpDo:            newDoctorReleaseHTTPClient().Do,
		validateCandidate: validateDoctorCandidateVersion,
	}
}

func cmdDoctor(args []string) {
	code := runDoctorCommand(args, os.Stdout, os.Stderr, defaultDoctorDeps())
	if code != 0 {
		os.Exit(code)
	}
}

func runDoctorCommand(args []string, stdout, stderr io.Writer, deps doctorDeps) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	fix := flags.Bool("fix", false, "repair safe filesystem and official-binary integrity problems")
	jsonOutput := flags.Bool("json", false, "emit a machine-readable diagnostic report")
	offline := flags.Bool("offline", false, "skip GitHub release-integrity checks")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "doctor does not accept positional arguments")
		return 2
	}
	report := runDoctor(doctorOptions{fix: *fix, offline: *offline}, deps)
	if err := writeDoctorReport(stdout, report, *jsonOutput); err != nil {
		fmt.Fprintf(stderr, "error writing doctor report: %v\n", err)
		return 1
	}
	if !report.OK {
		return 1
	}
	return 0
}

func runDoctor(options doctorOptions, deps doctorDeps) doctorReport {
	checks := []doctorCheck{
		checkDoctorExecutable(deps),
		checkDoctorPath(deps),
		checkDoctorVersion(deps),
		checkDoctorProvenance(deps),
	}

	home, err := deps.userHome()
	if err != nil || strings.TrimSpace(home) == "" {
		checks = append(checks,
			doctorCheck{ID: "state.shape", Status: doctorFail, Message: "cannot resolve the user home directory"},
			doctorCheck{ID: "state.permissions", Status: doctorFail, Message: "state permissions cannot be inspected"},
			doctorCheck{ID: "state.json", Status: doctorFail, Message: "state files cannot be inspected"},
			doctorCheck{ID: "state.local_models", Status: doctorFail, Message: "local models cannot be inspected"},
			doctorCheck{ID: "state.skill_approvals", Status: doctorFail, Message: "skill approvals cannot be inspected"},
			doctorCheck{ID: "dependencies.cli", Status: doctorFail, Message: "configured CLI dependencies cannot be inspected"},
		)
	} else {
		root := filepath.Join(home, ".veto")
		checks = append(checks, checkDoctorState(root, options, deps)...)
		checks = append(checks, checkDoctorJSON(root, deps))
		models, modelsErr := readDoctorModels(filepath.Join(root, "models.json"), deps.fs)
		checks = append(checks, checkDoctorModels(models, modelsErr))
		checks = append(checks, checkDoctorSkills(root, deps))
		checks = append(checks, checkDoctorDependencies(root, models, modelsErr, deps))
	}

	checks = append(checks, checkDoctorReleaseIntegrity(options, deps))
	return summarizeDoctorChecks(checks)
}

func summarizeDoctorChecks(checks []doctorCheck) doctorReport {
	report := doctorReport{Checks: checks, OK: true}
	for _, check := range checks {
		switch check.Status {
		case doctorPass:
			report.Summary.Pass++
		case doctorWarn:
			report.Summary.Warn++
		case doctorFail:
			report.Summary.Fail++
			report.OK = false
		case doctorFixed:
			report.Summary.Fixed++
		}
	}
	return report
}

func writeDoctorReport(w io.Writer, report doctorReport, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(w).Encode(report)
	}
	for _, check := range report.Checks {
		if _, err := fmt.Fprintf(w, "%-5s  %-24s  %s\n", check.Status, check.ID, check.Message); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "\n%d pass, %d warn, %d fail, %d fixed\n", report.Summary.Pass, report.Summary.Warn, report.Summary.Fail, report.Summary.Fixed)
	return err
}

func checkDoctorExecutable(deps doctorDeps) doctorCheck {
	path, err := deps.executable()
	if err != nil || path == "" {
		return doctorCheck{ID: "install.executable", Status: doctorFail, Message: "cannot resolve the running executable"}
	}
	info, err := deps.fs.Lstat(path)
	if err != nil {
		return doctorCheck{ID: "install.executable", Status: doctorFail, Message: "running executable cannot be inspected"}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return doctorCheck{ID: "install.executable", Status: doctorWarn, Message: "running executable is a symlink; automatic replacement is disabled"}
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return doctorCheck{ID: "install.executable", Status: doctorFail, Message: "running executable is not a regular executable file"}
	}
	return doctorCheck{ID: "install.executable", Status: doctorPass, Message: "running executable is a regular file"}
}

func checkDoctorPath(deps doctorDeps) doctorCheck {
	running, err := deps.executable()
	if err != nil || running == "" {
		return doctorCheck{ID: "install.path", Status: doctorWarn, Message: "PATH precedence cannot be compared"}
	}
	name := "veto"
	if deps.goos == "windows" {
		name = "veto.exe"
	}
	var matches []string
	for _, dir := range filepath.SplitList(deps.pathEnv()) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, name)
		info, statErr := deps.fs.Stat(candidate)
		if statErr == nil && info.Mode().IsRegular() && (deps.goos == "windows" || info.Mode().Perm()&0111 != 0) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return doctorCheck{ID: "install.path", Status: doctorWarn, Message: "veto is not discoverable on PATH"}
	}
	runningInfo, runningErr := deps.fs.Stat(running)
	firstInfo, firstErr := deps.fs.Stat(matches[0])
	firstIsRunning := runningErr == nil && firstErr == nil && os.SameFile(runningInfo, firstInfo)
	if len(matches) > 1 {
		message := fmt.Sprintf("PATH contains %d veto executables", len(matches))
		if !firstIsRunning {
			message += "; another binary takes precedence"
		}
		return doctorCheck{ID: "install.path", Status: doctorWarn, Message: message}
	}
	if !firstIsRunning {
		return doctorCheck{ID: "install.path", Status: doctorWarn, Message: "another veto executable takes precedence on PATH"}
	}
	return doctorCheck{ID: "install.path", Status: doctorPass, Message: "running executable is first on PATH"}
}

func checkDoctorVersion(deps doctorDeps) doctorCheck {
	resolved := resolveBuildVersion(deps.linkerVersion, deps.readBuildInfo)
	if resolved == "dev" {
		return doctorCheck{ID: "build.version", Status: doctorWarn, Message: "development version; no module release version is embedded"}
	}
	return doctorCheck{ID: "build.version", Status: doctorPass, Message: "version " + resolved}
}

func checkDoctorProvenance(deps doctorDeps) doctorCheck {
	if deps.buildProvenance == "official" {
		return doctorCheck{ID: "build.provenance", Status: doctorPass, Message: "official release build marker is present"}
	}
	resolved := resolveBuildVersion(deps.linkerVersion, deps.readBuildInfo)
	if resolved == "dev" {
		return doctorCheck{ID: "build.provenance", Status: doctorWarn, Message: "source/development build; release provenance is not claimed"}
	}
	return doctorCheck{ID: "build.provenance", Status: doctorWarn, Message: "versioned source or go-install build; release provenance is not claimed"}
}

type doctorStateInspection struct {
	missingManaged   int
	shapeFailures    int
	badPermissions   []doctorStateEntry
	rootMissing      bool
	ownershipKnown   bool
	permissionsKnown bool
}

type doctorStateEntry struct {
	path string
	mode os.FileMode
}

var doctorManagedDirs = []string{"skills", "plans", "checkpoints", "logs"}

func checkDoctorState(root string, options doctorOptions, deps doctorDeps) []doctorCheck {
	inspection := inspectDoctorState(root, deps.fs)
	created := 0
	if options.fix && inspection.shapeFailures == 0 && (inspection.rootMissing || inspection.missingManaged > 0) {
		for _, path := range append([]string{root}, doctorManagedPaths(root)...) {
			if _, err := deps.fs.Lstat(path); errors.Is(err, fs.ErrNotExist) {
				if mkdirErr := deps.fs.MkdirAll(path, 0700); mkdirErr == nil {
					_ = deps.fs.Chmod(path, 0700)
					created++
				}
			}
		}
		inspection = inspectDoctorState(root, deps.fs)
	}

	shape := doctorCheck{ID: "state.shape", Status: doctorPass, Message: "managed state entries have a safe ownership and file shape"}
	if inspection.shapeFailures > 0 {
		shape.Status = doctorFail
		shape.Message = fmt.Sprintf("%d state entries have unsafe ownership, symlinks, or file types", inspection.shapeFailures)
	} else if created > 0 {
		shape.Status = doctorFixed
		shape.Message = fmt.Sprintf("created %d missing managed directories", created)
	} else if inspection.rootMissing {
		shape.Status = doctorWarn
		shape.Message = "~/.veto is not present; no local state to inspect"
		shape.Repairable = true
	} else if inspection.missingManaged > 0 {
		shape.Status = doctorWarn
		shape.Message = fmt.Sprintf("%d managed directories are not present", inspection.missingManaged)
		shape.Repairable = true
	} else if !inspection.ownershipKnown {
		shape.Status = doctorWarn
		shape.Message = "state shape is safe; ownership verification is unavailable on this platform"
	}

	permissions := doctorCheck{ID: "state.permissions", Status: doctorPass, Message: "directories use 0700 and files use 0600"}
	if inspection.rootMissing {
		permissions.Status = doctorWarn
		permissions.Message = "state permissions are not applicable until ~/.veto exists"
		permissions.Repairable = true
	} else if !inspection.permissionsKnown {
		permissions.Status = doctorWarn
		permissions.Message = "POSIX state-permission verification is unavailable on this platform"
	} else if len(inspection.badPermissions) > 0 {
		permissions.Status = doctorFail
		permissions.Repairable = true
		permissions.Message = fmt.Sprintf("%d state entries have permissions other than 0700/0600", len(inspection.badPermissions))
		if options.fix && inspection.shapeFailures == 0 {
			fixed := 0
			for _, entry := range inspection.badPermissions {
				if err := deps.fs.Chmod(entry.path, entry.mode); err == nil {
					fixed++
				}
			}
			if fixed == len(inspection.badPermissions) {
				permissions.Status = doctorFixed
				permissions.Repairable = false
				permissions.Message = fmt.Sprintf("corrected permissions on %d state entries", fixed)
			}
		}
	}
	return []doctorCheck{shape, permissions}
}

func doctorManagedPaths(root string) []string {
	paths := make([]string, 0, len(doctorManagedDirs))
	for _, name := range doctorManagedDirs {
		paths = append(paths, filepath.Join(root, name))
	}
	return paths
}

func inspectDoctorState(root string, filesystem doctorFilesystem) doctorStateInspection {
	inspection := doctorStateInspection{ownershipKnown: true, permissionsKnown: true}
	rootInfo, err := filesystem.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		inspection.rootMissing = true
		inspection.missingManaged = len(doctorManagedDirs)
		return inspection
	}
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		inspection.shapeFailures++
		return inspection
	}
	inspectDoctorStateEntry(root, rootInfo, &inspection)
	for _, path := range doctorManagedPaths(root) {
		if _, err := filesystem.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			inspection.missingManaged++
		}
	}
	walkDoctorState(root, filesystem, &inspection)
	return inspection
}

func walkDoctorState(dir string, filesystem doctorFilesystem, inspection *doctorStateInspection) {
	entries, err := filesystem.ReadDir(dir)
	if err != nil {
		inspection.shapeFailures++
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, lstatErr := filesystem.Lstat(path)
		if lstatErr != nil {
			inspection.shapeFailures++
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			inspection.shapeFailures++
			continue
		}
		inspectDoctorStateEntry(path, info, inspection)
		if info.IsDir() {
			walkDoctorState(path, filesystem, inspection)
		}
	}
}

func inspectDoctorStateEntry(path string, info os.FileInfo, inspection *doctorStateInspection) {
	owned, supported := doctorFileOwnedByCurrentUser(info)
	if !supported {
		inspection.ownershipKnown = false
	} else if !owned {
		inspection.shapeFailures++
	}
	if !doctorStatePermissionsSupported() {
		inspection.permissionsKnown = false
		return
	}
	want := os.FileMode(0600)
	if info.IsDir() {
		want = 0700
	}
	if info.Mode().Perm() != want {
		inspection.badPermissions = append(inspection.badPermissions, doctorStateEntry{path: path, mode: want})
	}
}

func checkDoctorJSON(root string, deps doctorDeps) doctorCheck {
	paths := []string{
		filepath.Join(root, "credentials.json"),
		filepath.Join(root, "config.json"),
		filepath.Join(root, "models.json"),
		filepath.Join(root, "history.json"),
	}
	checkpointDir := filepath.Join(root, "checkpoints")
	if entries, err := deps.fs.ReadDir(checkpointDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				paths = append(paths, filepath.Join(checkpointDir, entry.Name()))
			}
		}
	}
	invalid := 0
	valid := 0
	for _, path := range paths {
		info, err := deps.fs.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		data, readErr := readDoctorFileLimited(deps.fs, path, doctorStateFileMaxBytes)
		if readErr != nil || !json.Valid(data) {
			invalid++
		} else {
			valid++
		}
	}
	if invalid > 0 {
		return doctorCheck{ID: "state.json", Status: doctorFail, Message: fmt.Sprintf("%d managed JSON file(s) are unreadable or malformed", invalid)}
	}
	return doctorCheck{ID: "state.json", Status: doctorPass, Message: fmt.Sprintf("%d managed JSON file(s) are valid", valid)}
}

func readDoctorModels(path string, filesystem doctorFilesystem) ([]LocalModel, error) {
	info, err := filesystem.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("models file is not a regular file")
	}
	data, err := readDoctorFileLimited(filesystem, path, doctorStateFileMaxBytes)
	if err != nil {
		return nil, err
	}
	var models []LocalModel
	if err := json.Unmarshal(data, &models); err != nil {
		return nil, err
	}
	return models, nil
}

func checkDoctorModels(models []LocalModel, readErr error) doctorCheck {
	if readErr != nil {
		return doctorCheck{ID: "state.local_models", Status: doctorFail, Message: "local-model definitions are unreadable or malformed"}
	}
	builtins := make(map[string]bool)
	for _, model := range routerModelNames() {
		builtins[model] = true
	}
	seen := make(map[string]bool, len(models))
	invalid := 0
	for _, model := range models {
		if seen[model.Name] || validateDoctorLocalModel(model, builtins) != nil {
			invalid++
		}
		seen[model.Name] = true
	}
	if invalid > 0 {
		return doctorCheck{ID: "state.local_models", Status: doctorFail, Message: fmt.Sprintf("%d local-model definition(s) are invalid", invalid)}
	}
	return doctorCheck{ID: "state.local_models", Status: doctorPass, Message: fmt.Sprintf("%d local-model definition(s) are valid", len(models))}
}

func routerModelNames() []string {
	var names []string
	for _, model := range router.NewRegistry().All() {
		names = append(names, model.Name)
	}
	return names
}

func validateDoctorLocalModel(model LocalModel, builtins map[string]bool) error {
	if err := validateLocalModel(model, builtins); err != nil {
		return err
	}
	parsed, err := url.Parse(model.Endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return errors.New("endpoint must be an http or https URL without embedded credentials")
	}
	return nil
}

func checkDoctorSkills(root string, deps doctorDeps) doctorCheck {
	configPath := filepath.Join(root, "config.json")
	info, err := deps.fs.Lstat(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return doctorCheck{ID: "state.skill_approvals", Status: doctorPass, Message: "no external skill paths are approved"}
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return doctorCheck{ID: "state.skill_approvals", Status: doctorFail, Message: "skill approvals cannot be read safely"}
	}
	data, err := readDoctorFileLimited(deps.fs, configPath, doctorStateFileMaxBytes)
	if err != nil {
		return doctorCheck{ID: "state.skill_approvals", Status: doctorFail, Message: "skill approvals cannot be read safely"}
	}
	var full map[string]json.RawMessage
	if err := json.Unmarshal(data, &full); err != nil {
		return doctorCheck{ID: "state.skill_approvals", Status: doctorFail, Message: "skill approval configuration is malformed"}
	}
	var config skillsConfig
	if raw, ok := full["skills"]; ok {
		if err := json.Unmarshal(raw, &config); err != nil {
			return doctorCheck{ID: "state.skill_approvals", Status: doctorFail, Message: "skill approval configuration is malformed"}
		}
	}
	invalid := 0
	for _, path := range config.ApprovedDirs {
		if !filepath.IsAbs(path) || doctorPathHasSymlink(path, deps.fs) {
			invalid++
			continue
		}
		pathInfo, pathErr := deps.fs.Stat(path)
		if pathErr != nil || !pathInfo.IsDir() {
			invalid++
		}
	}
	for _, path := range config.ApprovedFiles {
		if !filepath.IsAbs(path) || !strings.HasSuffix(strings.ToLower(path), ".md") || doctorPathHasSymlink(path, deps.fs) {
			invalid++
			continue
		}
		pathInfo, pathErr := deps.fs.Stat(path)
		if pathErr != nil || !pathInfo.Mode().IsRegular() {
			invalid++
		}
	}
	total := len(config.ApprovedDirs) + len(config.ApprovedFiles)
	if invalid > 0 {
		return doctorCheck{ID: "state.skill_approvals", Status: doctorFail, Message: fmt.Sprintf("%d approved skill path(s) are missing, relative, symlinked, or the wrong type", invalid)}
	}
	return doctorCheck{ID: "state.skill_approvals", Status: doctorPass, Message: fmt.Sprintf("%d approved external skill path(s) are safe", total)}
}

func doctorPathHasSymlink(path string, filesystem doctorFilesystem) bool {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	current := volume + string(os.PathSeparator)
	for _, part := range strings.Split(strings.TrimPrefix(rest, string(os.PathSeparator)), string(os.PathSeparator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := filesystem.Lstat(current)
		if err != nil {
			return false
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

func checkDoctorDependencies(root string, models []LocalModel, modelsErr error, deps doctorDeps) doctorCheck {
	required := make(map[string]bool)
	credentialsPath := filepath.Join(root, "credentials.json")
	if info, err := deps.fs.Lstat(credentialsPath); err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		data, readErr := readDoctorFileLimited(deps.fs, credentialsPath, doctorStateFileMaxBytes)
		var credentials map[string]string
		if readErr == nil && json.Unmarshal(data, &credentials) == nil && credentials["CLAUDE_SUBSCRIPTION"] == "true" {
			required["claude"] = true
		}
	}
	if modelsErr == nil {
		for _, model := range models {
			parsed, _ := url.Parse(model.Endpoint)
			if strings.HasPrefix(model.Name, "ollama-") || (parsed != nil && parsed.Port() == "11434") {
				required["ollama"] = true
			}
		}
	}
	var missing []string
	for name := range required {
		if _, err := deps.lookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return doctorCheck{ID: "dependencies.cli", Status: doctorFail, Message: fmt.Sprintf("%d configured CLI dependenc%s missing from PATH", len(missing), pluralY(len(missing)))}
	}
	return doctorCheck{ID: "dependencies.cli", Status: doctorPass, Message: fmt.Sprintf("%d configured CLI dependenc%s available", len(required), pluralY(len(required)))}
}

func pluralY(count int) string {
	if count == 1 {
		return "y is"
	}
	return "ies are"
}
