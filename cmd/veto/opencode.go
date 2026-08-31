package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	opencodeintegration "github.com/oleg-koval/veto/integrations/opencode"
	opencodert "github.com/oleg-koval/veto/pkg/opencode"
)

const openCodeCommandTimeout = 10 * time.Second

func cmdOpenCode(args []string) {
	code := runOpenCodeCommand(args, os.Stdout, os.Stderr, opencodert.DefaultDependencies(), vetoCfgPath())
	if code != 0 {
		os.Exit(code)
	}
}

func runOpenCodeCommand(args []string, stdout, stderr io.Writer, deps opencodert.Dependencies, configPath string) int {
	if len(args) == 0 {
		printOpenCodeUsage(stderr)
		return 2
	}
	switch args[0] {
	case "connect":
		return runOpenCodeConnect(args[1:], stdout, stderr, deps, configPath)
	case "status":
		return runOpenCodeStatus(args[1:], stdout, stderr, deps, configPath)
	case "disconnect":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "opencode disconnect does not accept arguments")
			return 2
		}
		if err := removeOpenCodeConfig(configPath); err != nil {
			fmt.Fprintf(stderr, "disconnect OpenCode: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "OpenCode disconnected from Veto; OpenCode credentials were not changed.")
		return 0
	case "plugin":
		return runOpenCodePlugin(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		printOpenCodeUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown opencode command: %s\n", args[0])
		printOpenCodeUsage(stderr)
		return 2
	}
}

func runOpenCodePlugin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: veto opencode plugin <install|status|uninstall> [--config-dir PATH] [--force]")
		return 2
	}
	flags := flag.NewFlagSet("opencode plugin "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", "", "OpenCode configuration directory")
	force := flags.Bool("force", false, "replace conflicting integration files (install only)")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || (*force && args[0] != "install") {
		fmt.Fprintln(stderr, "unexpected OpenCode plugin arguments")
		return 2
	}
	dir, err := openCodeConfigDir(*configDir)
	if err != nil {
		fmt.Fprintf(stderr, "resolve OpenCode config directory: %v\n", err)
		return 1
	}
	var state opencodeintegration.State
	switch args[0] {
	case "install":
		state, err = opencodeintegration.Install(dir, *force)
	case "status":
		state, err = opencodeintegration.Status(dir)
	case "uninstall":
		state, err = opencodeintegration.Uninstall(dir)
	default:
		fmt.Fprintf(stderr, "unknown OpenCode plugin command: %s\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "OpenCode plugin %s: %v\n", args[0], err)
		return 1
	}
	if args[0] == "install" {
		fmt.Fprintf(stdout, "Veto OpenCode integration installed in %s (%d files). Restart OpenCode to load it.\n", dir, state.Installed)
	} else if args[0] == "uninstall" {
		fmt.Fprintf(stdout, "Veto OpenCode integration removed from %s.\n", dir)
	} else if state.Missing == 0 && state.Modified == 0 {
		fmt.Fprintf(stdout, "Veto OpenCode integration is installed and current in %s (%d files).\n", dir, state.Installed)
	} else {
		fmt.Fprintf(stdout, "Veto OpenCode integration is not current in %s (installed=%d missing=%d modified=%d).\n", dir, state.Installed, state.Missing, state.Modified)
		return 1
	}
	return 0
}

func openCodeConfigDir(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	if configured := os.Getenv("OPENCODE_CONFIG_DIR"); configured != "" {
		return filepath.Abs(configured)
	}
	if configured := os.Getenv("XDG_CONFIG_HOME"); configured != "" {
		base, err := filepath.Abs(configured)
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "opencode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "opencode"), nil
}

func runOpenCodeConnect(args []string, stdout, stderr io.Writer, deps opencodert.Dependencies, configPath string) int {
	flags := flag.NewFlagSet("opencode connect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	server := flags.String("server", "", "explicit OpenCode loopback server URL")
	managed := flags.Bool("managed", false, "let Veto start an authenticated local OpenCode server when needed")
	cli := flags.Bool("cli", false, "use the OpenCode CLI fallback")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || (*cli && (*managed || *server != "")) {
		fmt.Fprintln(stderr, "use --cli, --server URL, or --managed [--server URL]")
		return 2
	}
	config := opencodert.Config{Mode: opencodert.ModeCLI}
	switch {
	case *managed:
		config = opencodert.Config{Mode: opencodert.ModeManaged, Server: *server}
	case *server != "":
		config = opencodert.Config{Mode: opencodert.ModeAttach, Server: *server}
	case *cli:
		config.Mode = opencodert.ModeCLI
	}
	ctx, cancel := context.WithTimeout(context.Background(), openCodeCommandTimeout)
	defer cancel()
	discovery, err := opencodert.Discover(ctx, config, deps)
	if err != nil {
		fmt.Fprintf(stderr, "connect OpenCode: %v\n", err)
		return 1
	}
	if err := saveOpenCodeConfig(configPath, config); err != nil {
		fmt.Fprintf(stderr, "save OpenCode connection: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "OpenCode connected via %s (version %s, %d model(s)).\n", config.Mode, discovery.Version, len(discovery.Models))
	return 0
}

func runOpenCodeStatus(args []string, stdout, stderr io.Writer, deps opencodert.Dependencies, configPath string) int {
	flags := flag.NewFlagSet("opencode status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable status")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "opencode status does not accept positional arguments")
		return 2
	}
	config, configured, err := loadOpenCodeConfig(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "read OpenCode connection: %v\n", err)
		return 1
	}
	if !configured {
		fmt.Fprintln(stderr, "OpenCode is not connected; run 'veto opencode connect'.")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), openCodeCommandTimeout)
	defer cancel()
	var discovery opencodert.Discovery
	if config.Mode == opencodert.ModeManaged {
		discovery, err = opencodert.ProbeCLI(ctx, deps)
		discovery.Mode = opencodert.ModeManaged
		discovery.Server = config.Server
	} else {
		discovery, err = opencodert.Discover(ctx, config, deps)
	}
	if err != nil {
		fmt.Fprintf(stderr, "OpenCode connection is unhealthy: %v\n", err)
		return 1
	}
	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(discovery); err != nil {
			fmt.Fprintf(stderr, "write OpenCode status: %v\n", err)
			return 1
		}
		return 0
	}
	if config.Mode == opencodert.ModeManaged {
		fmt.Fprintf(stdout, "OpenCode %s is ready for managed mode; Veto starts the local server on demand.\n", discovery.Version)
		return 0
	}
	fmt.Fprintf(stdout, "OpenCode %s is healthy via %s; %d model(s) discovered.\n", discovery.Version, config.Mode, len(discovery.Models))
	return 0
}

func printOpenCodeUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: veto opencode <connect|status|disconnect|plugin>")
	fmt.Fprintln(w, "  veto opencode connect                         # CLI fallback")
	fmt.Fprintln(w, "  veto opencode connect --server http://127.0.0.1:4096")
	fmt.Fprintln(w, "  veto opencode connect --managed [--server http://127.0.0.1:4096]")
	fmt.Fprintln(w, "  veto opencode status [--json]")
	fmt.Fprintln(w, "  veto opencode disconnect")
	fmt.Fprintln(w, "  veto opencode plugin install [--config-dir PATH] [--force]")
	fmt.Fprintln(w, "  veto opencode plugin status [--config-dir PATH]")
	fmt.Fprintln(w, "  veto opencode plugin uninstall [--config-dir PATH]")
}

func loadOpenCodeConfig(path string) (opencodert.Config, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return opencodert.Config{}, false, nil
	}
	if err != nil {
		return opencodert.Config{}, false, err
	}
	return decodeOpenCodeConfig(data)
}

func decodeOpenCodeConfig(data []byte) (opencodert.Config, bool, error) {
	var full map[string]json.RawMessage
	if err := json.Unmarshal(data, &full); err != nil {
		return opencodert.Config{}, false, errors.New("config.json is malformed; refusing to guess or rewrite it")
	}
	if full == nil {
		return opencodert.Config{}, false, errors.New("config.json must contain a JSON object")
	}
	raw, ok := full["opencode"]
	if !ok {
		return opencodert.Config{}, false, nil
	}
	var config opencodert.Config
	if err := json.Unmarshal(raw, &config); err != nil {
		return opencodert.Config{}, false, errors.New("config.json has an invalid opencode section")
	}
	if err := validateOpenCodeConfig(config); err != nil {
		return opencodert.Config{}, false, err
	}
	return config, true, nil
}

func validateOpenCodeConfig(config opencodert.Config) error {
	switch config.Mode {
	case opencodert.ModeAttach:
		if _, err := opencodert.ValidateServerURL(config.Server); err != nil {
			return err
		}
	case opencodert.ModeManaged:
		if strings.TrimSpace(config.Server) != "" {
			if _, err := opencodert.ValidateServerURL(config.Server); err != nil {
				return err
			}
		}
	case opencodert.ModeCLI:
		if strings.TrimSpace(config.Server) != "" {
			return errors.New("OpenCode CLI mode must not configure a server URL")
		}
	default:
		return fmt.Errorf("unsupported OpenCode mode %q", config.Mode)
	}
	return nil
}

func saveOpenCodeConfig(path string, config opencodert.Config) error {
	if err := validateOpenCodeConfig(config); err != nil {
		return err
	}
	return mutateVetoConfig(path, func(full map[string]json.RawMessage) error {
		raw, err := json.Marshal(config)
		if err != nil {
			return err
		}
		full["opencode"] = raw
		return nil
	})
}

func removeOpenCodeConfig(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return mutateVetoConfig(path, func(full map[string]json.RawMessage) error {
		delete(full, "opencode")
		return nil
	})
}

func mutateVetoConfig(path string, mutate func(map[string]json.RawMessage) error) error {
	full := make(map[string]json.RawMessage)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("config.json must be a regular file, not a symlink")
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if json.Unmarshal(data, &full) != nil || full == nil {
			return errors.New("config.json is malformed; refusing to rewrite it")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := mutate(full); err != nil {
		return err
	}
	out, err := json.MarshalIndent(full, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(out); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
