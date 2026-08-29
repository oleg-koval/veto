package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	latestReleaseURL       = "https://api.github.com/repos/oleg-koval/veto/releases/latest"
	latestReleaseMaxBytes  = 64 << 10
	updateRequestTimeout   = 3 * time.Second
	updateCacheFileVersion = 1
)

type persistedUpdateCache struct {
	Version int `json:"version"`
	updateCache
}

type updateInstallDeps struct {
	doctor     doctorDeps
	lookPath   func(string) (string, error)
	runCommand func(context.Context, string, ...string) error
}

func maybeOfferAutomaticUpdate(args []string) bool {
	if !shouldOfferAutomaticUpdate(
		args,
		term.IsTerminal(int(os.Stdin.Fd())),
		term.IsTerminal(int(os.Stderr.Fd())),
	) {
		return false
	}

	home, homeErr := os.UserHomeDir()
	cachePath := ""
	if homeErr == nil && home != "" {
		cachePath = filepath.Join(home, ".veto", "update.json")
	}
	doctor := defaultDoctorDeps()
	deps := updateCheckDeps{
		now:            time.Now,
		currentVersion: resolvedVersion,
		readCache: func() (updateCache, error) {
			if cachePath == "" {
				return updateCache{}, errors.New("update cache unavailable")
			}
			return readUpdateCacheFile(cachePath)
		},
		writeCache: func(cache updateCache) error {
			if cachePath == "" {
				return errors.New("update cache unavailable")
			}
			return writeUpdateCacheFile(cachePath, cache)
		},
		fetchLatest: func(ctx context.Context) (string, error) {
			requestCtx, cancel := context.WithTimeout(ctx, updateRequestTimeout)
			defer cancel()
			return fetchLatestReleaseVersion(requestCtx, newUpdateHTTPClient().Do)
		},
		install: func(ctx context.Context, version string) error {
			return installUpdate(ctx, version, updateInstallDeps{
				doctor:   doctor,
				lookPath: exec.LookPath,
				runCommand: func(ctx context.Context, name string, args ...string) error {
					command := exec.CommandContext(ctx, name, args...)
					command.Stdin = os.Stdin
					command.Stdout = os.Stderr
					command.Stderr = os.Stderr
					return command.Run()
				},
			})
		},
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runAutomaticUpdate(ctx, os.Stdin, os.Stderr, deps)
}

func fetchLatestReleaseVersion(ctx context.Context, httpDo func(*http.Request) (*http.Response, error)) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "veto-update-check")
	response, err := httpDo(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest release returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > latestReleaseMaxBytes {
		return "", errors.New("latest release response is too large")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, latestReleaseMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > latestReleaseMaxBytes {
		return "", errors.New("latest release response is too large")
	}
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", errors.New("latest release response is malformed")
	}
	version := strings.TrimPrefix(release.TagName, "v")
	if _, err := parseStableVersion(version); err != nil {
		return "", errors.New("latest release tag is not a stable semantic version")
	}
	requiredAssets := map[string]bool{
		"SHA256SUMS": true, "BINARY_SHA256SUMS": true,
		"veto_" + version + "_darwin_arm64.tar.gz": true,
		"veto_" + version + "_darwin_amd64.tar.gz": true,
		"veto_" + version + "_linux_arm64.tar.gz":  true,
		"veto_" + version + "_linux_amd64.tar.gz":  true,
		"veto_" + version + "_windows_arm64.zip":   true,
		"veto_" + version + "_windows_amd64.zip":   true,
	}
	for _, asset := range release.Assets {
		delete(requiredAssets, asset.Name)
	}
	if len(requiredAssets) != 0 {
		return "", errors.New("latest release artifact set is incomplete")
	}
	return version, nil
}

func newUpdateHTTPClient() *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		transport = transport.Clone()
	} else {
		transport = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	return &http.Client{
		Transport: transport,
		Timeout:   updateRequestTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many update redirects")
			}
			host := strings.ToLower(request.URL.Hostname())
			trusted := host == "api.github.com" || host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com")
			if request.URL.Scheme != "https" || !trusted || request.URL.User != nil {
				return errors.New("update redirect left GitHub-owned HTTPS hosts")
			}
			return nil
		},
	}
}

func readUpdateCacheFile(path string) (updateCache, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return updateCache{}, err
	}
	if !info.Mode().IsRegular() {
		return updateCache{}, errors.New("update cache is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return updateCache{}, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, latestReleaseMaxBytes+1))
	if err != nil || len(body) > latestReleaseMaxBytes {
		return updateCache{}, errors.New("update cache is unreadable")
	}
	var persisted persistedUpdateCache
	if err := json.Unmarshal(body, &persisted); err != nil || persisted.Version != updateCacheFileVersion {
		return updateCache{}, errors.New("update cache is malformed")
	}
	return persisted.updateCache, nil
}

func writeUpdateCacheFile(path string, cache updateCache) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(persistedUpdateCache{Version: updateCacheFileVersion, updateCache: cache}, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".update-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(append(body, '\n')); err != nil {
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
	if err := os.Rename(tempPath, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return err
		}
		return os.Rename(tempPath, path)
	}
	return nil
}

func installUpdate(ctx context.Context, version string, deps updateInstallDeps) error {
	if _, err := parseStableVersion(version); err != nil {
		return err
	}
	executablePath, err := deps.doctor.executable()
	if err != nil || executablePath == "" {
		return errors.New("cannot resolve the running veto executable")
	}
	if homebrewManagedPath(executablePath) {
		brew, err := deps.lookPath("brew")
		if err != nil {
			return errors.New("homebrew installation detected, but brew is not on PATH")
		}
		if err := deps.runCommand(ctx, brew, "upgrade", "oleg-koval/tap/veto"); err != nil {
			return fmt.Errorf("brew upgrade failed: %w", err)
		}
		return nil
	}
	if doctorPackageManagedPath(executablePath) {
		return errors.New("package-managed installation detected; update it with its package manager")
	}
	if deps.doctor.buildProvenance != "official" {
		goBinary, err := deps.lookPath("go")
		if err != nil {
			return fmt.Errorf("source installation detected; run: go install github.com/oleg-koval/veto/cmd/veto@v%s", version)
		}
		if err := deps.runCommand(ctx, goBinary, "install", "github.com/oleg-koval/veto/cmd/veto@v"+version); err != nil {
			return fmt.Errorf("go install failed: %w", err)
		}
		if deps.doctor.validateCandidate == nil || deps.doctor.validateCandidate(executablePath, version) != nil {
			return fmt.Errorf("go install completed, but the running veto path was not replaced; run: go install github.com/oleg-koval/veto/cmd/veto@v%s", version)
		}
		return nil
	}

	allowed, reason := doctorReplacementAllowed(executablePath, deps.doctor.fs)
	if !allowed {
		return errors.New("automatic replacement refused: " + reason)
	}
	binaryAssetPath := doctorBinaryAssetPath(version, deps.doctor.goos, deps.doctor.goarch)
	manifest, err := fetchDoctorReleaseAsset(ctx, deps.doctor, version, "BINARY_SHA256SUMS", doctorManifestMaxBytes)
	if err != nil {
		return errors.New("new release binary checksum manifest is unavailable or untrusted")
	}
	expected, err := parseDoctorChecksum(manifest, binaryAssetPath)
	if err != nil {
		return errors.New("new release binary checksum manifest is malformed or incomplete")
	}
	current, err := readDoctorFileLimited(deps.doctor.fs, executablePath, doctorBinaryMaxBytes)
	if err != nil {
		return errors.New("running executable cannot be read")
	}
	staged, err := repairDoctorExecutable(ctx, deps.doctor, executablePath, version, expected, current)
	if err != nil {
		return err
	}
	if staged != "" {
		return fmt.Errorf("verified Windows replacement staged at %s; close veto and replace the executable manually", staged)
	}
	return nil
}

func homebrewManagedPath(path string) bool {
	clean := strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	return strings.Contains(clean, "/homebrew/") || strings.Contains(clean, "/cellar/") || strings.Contains(clean, "/linuxbrew/")
}
