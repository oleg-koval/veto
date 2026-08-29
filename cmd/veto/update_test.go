package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldOfferAutomaticUpdateOnlyForInteractiveHumanOutput(t *testing.T) {
	tests := []struct {
		name                string
		args                []string
		stdinTTY, stderrTTY bool
		want                bool
	}{
		{name: "interactive", args: []string{"route", "task"}, stdinTTY: true, stderrTTY: true, want: true},
		{name: "piped input", args: []string{"route", "task"}, stderrTTY: true},
		{name: "piped errors", args: []string{"route", "task"}, stdinTTY: true},
		{name: "json", args: []string{"route", "--json", "task"}, stdinTTY: true, stderrTTY: true},
		{name: "single-dash json", args: []string{"route", "-json", "task"}, stdinTTY: true, stderrTTY: true},
		{name: "quiet", args: []string{"run", "--quiet", "task"}, stdinTTY: true, stderrTTY: true},
		{name: "single-dash quiet", args: []string{"run", "-quiet", "task"}, stdinTTY: true, stderrTTY: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, shouldOfferAutomaticUpdate(test.args, test.stdinTTY, test.stderrTTY))
		})
	}
}

func TestRunAutomaticUpdatePromptsAndInstallsNewRelease(t *testing.T) {
	now := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	installed := ""
	written := updateCache{}
	deps := updateCheckDeps{
		now:            func() time.Time { return now },
		currentVersion: func() string { return "0.1.0" },
		readCache:      func() (updateCache, error) { return updateCache{}, errors.New("missing") },
		writeCache:     func(cache updateCache) error { written = cache; return nil },
		fetchLatest:    func(context.Context) (string, error) { return "0.1.1", nil },
		install:        func(_ context.Context, version string) error { installed = version; return nil },
	}
	var output bytes.Buffer

	updated := runAutomaticUpdate(t.Context(), bytes.NewBufferString("y\n"), &output, deps)

	assert.True(t, updated)
	assert.Equal(t, "0.1.1", installed)
	assert.Contains(t, output.String(), "veto 0.1.1 is available")
	assert.Equal(t, "0.1.1", written.PromptedVersion)
	assert.Equal(t, now, written.PromptedAt)
}

func TestRunAutomaticUpdateUsesFreshCacheAndDoesNotNagTwice(t *testing.T) {
	now := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	fetched := false
	installed := false
	deps := updateCheckDeps{
		now:            func() time.Time { return now },
		currentVersion: func() string { return "0.1.0" },
		readCache: func() (updateCache, error) {
			return updateCache{
				CheckedAt:       now.Add(-time.Hour),
				LatestVersion:   "0.1.1",
				PromptedAt:      now.Add(-time.Hour),
				PromptedVersion: "0.1.1",
			}, nil
		},
		writeCache:  func(updateCache) error { return nil },
		fetchLatest: func(context.Context) (string, error) { fetched = true; return "", nil },
		install:     func(context.Context, string) error { installed = true; return nil },
	}
	var output bytes.Buffer

	updated := runAutomaticUpdate(t.Context(), bytes.NewBufferString("y\n"), &output, deps)

	assert.False(t, updated)
	assert.False(t, fetched)
	assert.False(t, installed)
	assert.Empty(t, output.String())
}

func TestRunAutomaticUpdateDoesNotTrustFutureCacheTimestamps(t *testing.T) {
	now := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	fetched := false
	deps := updateCheckDeps{
		now:            func() time.Time { return now },
		currentVersion: func() string { return "0.1.0" },
		readCache: func() (updateCache, error) {
			return updateCache{CheckedAt: now.Add(time.Hour), LatestVersion: "0.1.0"}, nil
		},
		writeCache: func(updateCache) error { return nil },
		fetchLatest: func(context.Context) (string, error) {
			fetched = true
			return "0.1.0", nil
		},
		install: func(context.Context, string) error { return errors.New("must not run") },
	}

	assert.False(t, runAutomaticUpdate(t.Context(), bytes.NewBuffer(nil), io.Discard, deps))
	assert.True(t, fetched)
}

func TestRunAutomaticUpdateFailsOpenWhenCheckFails(t *testing.T) {
	deps := updateCheckDeps{
		now:            time.Now,
		currentVersion: func() string { return "0.1.0" },
		readCache:      func() (updateCache, error) { return updateCache{}, errors.New("missing") },
		writeCache:     func(updateCache) error { return nil },
		fetchLatest:    func(context.Context) (string, error) { return "", errors.New("offline") },
		install:        func(context.Context, string) error { return errors.New("must not run") },
	}
	var output bytes.Buffer

	assert.False(t, runAutomaticUpdate(t.Context(), bytes.NewBuffer(nil), &output, deps))
	assert.Empty(t, output.String())
}

func TestRunAutomaticUpdateSkipsDevelopmentBuildWithoutFetching(t *testing.T) {
	fetched := false
	deps := updateCheckDeps{
		now:            time.Now,
		currentVersion: func() string { return "dev" },
		readCache:      func() (updateCache, error) { return updateCache{}, errors.New("missing") },
		writeCache:     func(updateCache) error { return nil },
		fetchLatest:    func(context.Context) (string, error) { fetched = true; return "0.1.1", nil },
		install:        func(context.Context, string) error { return errors.New("must not run") },
	}

	assert.False(t, runAutomaticUpdate(t.Context(), bytes.NewBuffer(nil), io.Discard, deps))
	assert.False(t, fetched)
}

func TestNewerStableVersion(t *testing.T) {
	for _, test := range []struct {
		current, latest string
		want, wantErr   bool
	}{
		{current: "0.1.0", latest: "0.1.1", want: true},
		{current: "0.9.9", latest: "1.0.0", want: true},
		{current: "0.2.0", latest: "0.1.9"},
		{current: "0.1.1", latest: "0.1.1"},
		{current: "dev", latest: "0.1.1", wantErr: true},
		{current: "0.1.0", latest: "../../latest", wantErr: true},
	} {
		t.Run(test.current+"-to-"+test.latest, func(t *testing.T) {
			got, err := newerStableVersion(test.current, test.latest)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

type updateTestRoundTripper struct{}

func (updateTestRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

func TestNewUpdateHTTPClientHandlesReplacedDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = updateTestRoundTripper{}
	t.Cleanup(func() { http.DefaultTransport = original })

	assert.NotPanics(t, func() {
		client := newUpdateHTTPClient()
		_, ok := client.Transport.(*http.Transport)
		assert.True(t, ok)
	})
}

func TestNewUpdateHTTPClientRejectsUntrustedRedirects(t *testing.T) {
	client := newUpdateHTTPClient()
	tests := []struct {
		name    string
		url     string
		via     int
		wantErr bool
	}{
		{name: "GitHub HTTPS", url: "https://github.com/oleg-koval/veto", wantErr: false},
		{name: "off allowlist", url: "https://example.com/release", wantErr: true},
		{name: "plain HTTP", url: "http://github.com/oleg-koval/veto", wantErr: true},
		{name: "userinfo", url: "https://user@github.com/oleg-koval/veto", wantErr: true},
		{name: "three redirects", url: "https://github.com/oleg-koval/veto", via: 3, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, test.url, nil)
			require.NoError(t, err)
			via := make([]*http.Request, test.via)
			err = client.CheckRedirect(request, via)
			if test.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestFetchLatestReleaseVersionUsesBoundedOfficialEndpoint(t *testing.T) {
	requested := ""
	version, err := fetchLatestReleaseVersion(t.Context(), func(request *http.Request) (*http.Response, error) {
		requested = request.URL.String()
		assert.Equal(t, "application/vnd.github+json", request.Header.Get("Accept"))
		assert.Equal(t, "2022-11-28", request.Header.Get("X-GitHub-Api-Version"))
		body, marshalErr := json.Marshal(map[string]any{
			"tag_name": "v0.1.1",
			"assets": []map[string]string{
				{"name": "SHA256SUMS"},
				{"name": "BINARY_SHA256SUMS"},
				{"name": "veto_0.1.1_darwin_arm64.tar.gz"},
				{"name": "veto_0.1.1_darwin_amd64.tar.gz"},
				{"name": "veto_0.1.1_linux_arm64.tar.gz"},
				{"name": "veto_0.1.1_linux_amd64.tar.gz"},
				{"name": "veto_0.1.1_windows_arm64.zip"},
				{"name": "veto_0.1.1_windows_amd64.zip"},
			},
		})
		require.NoError(t, marshalErr)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, "0.1.1", version)
	assert.Equal(t, "https://api.github.com/repos/oleg-koval/veto/releases/latest", requested)
}

func TestFetchLatestReleaseVersionRejectsMalformedTag(t *testing.T) {
	_, err := fetchLatestReleaseVersion(t.Context(), func(*http.Request) (*http.Response, error) {
		body := []byte(`{"tag_name":"../../latest"}`)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}, nil
	})
	assert.Error(t, err)
}

func TestFetchLatestReleaseVersionIgnoresReleaseUntilArtifactsArePublished(t *testing.T) {
	_, err := fetchLatestReleaseVersion(t.Context(), func(*http.Request) (*http.Response, error) {
		body := []byte(`{"tag_name":"v0.1.1","assets":[]}`)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}, nil
	})
	assert.ErrorContains(t, err, "incomplete")
}

func TestUpdateCacheRoundTripUsesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".veto", "update.json")
	want := updateCache{CheckedAt: time.Now().UTC().Truncate(time.Second), LatestVersion: "0.1.1"}

	require.NoError(t, writeUpdateCacheFile(path, want))
	got, err := readUpdateCacheFile(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestReadUpdateCacheRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	link := filepath.Join(dir, "update.json")
	require.NoError(t, writeUpdateCacheFile(target, updateCache{CheckedAt: time.Now(), LatestVersion: "0.1.1"}))
	require.NoError(t, os.Symlink(target, link))

	_, err := readUpdateCacheFile(link)
	assert.ErrorContains(t, err, "regular file")
}

func TestInstallUpdateUsesHomebrewForHomebrewBinary(t *testing.T) {
	var command string
	var args []string
	deps := updateInstallDeps{
		doctor:   doctorDeps{executable: func() (string, error) { return "/opt/homebrew/bin/veto", nil }},
		lookPath: func(name string) (string, error) { return "/opt/homebrew/bin/" + name, nil },
		runCommand: func(_ context.Context, name string, commandArgs ...string) error {
			command, args = name, commandArgs
			return nil
		},
	}

	require.NoError(t, installUpdate(t.Context(), "0.1.1", deps))
	assert.Equal(t, "/opt/homebrew/bin/brew", command)
	assert.Equal(t, []string{"upgrade", "oleg-koval/tap/veto"}, args)
}

func TestInstallUpdateUsesGoInstallForSourceBuild(t *testing.T) {
	var args []string
	deps := updateInstallDeps{
		doctor: doctorDeps{
			executable:        func() (string, error) { return "/Users/test/go/bin/veto", nil },
			buildProvenance:   "source",
			validateCandidate: func(path, version string) error { return nil },
		},
		lookPath: func(string) (string, error) { return "/usr/local/bin/go", nil },
		runCommand: func(_ context.Context, _ string, commandArgs ...string) error {
			args = commandArgs
			return nil
		},
	}

	require.NoError(t, installUpdate(t.Context(), "0.1.1", deps))
	assert.Equal(t, []string{"install", "github.com/oleg-koval/veto/cmd/veto@v0.1.1"}, args)
}

func TestInstallUpdateRejectsGoInstallThatDidNotReplaceRunningBinary(t *testing.T) {
	deps := updateInstallDeps{
		doctor: doctorDeps{
			executable:        func() (string, error) { return "/Users/test/bin/veto", nil },
			buildProvenance:   "source",
			validateCandidate: func(path, version string) error { return errors.New("still 0.1.0") },
		},
		lookPath:   func(string) (string, error) { return "/usr/local/bin/go", nil },
		runCommand: func(context.Context, string, ...string) error { return nil },
	}

	err := installUpdate(t.Context(), "0.1.1", deps)
	assert.ErrorContains(t, err, "running veto path was not replaced")
}

func TestInstallUpdateReplacesOfficialStandaloneWithVerifiedRelease(t *testing.T) {
	current := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	candidate := []byte("veto 0.1.1 candidate")
	archiveName := "veto_0.1.1_darwin_arm64.tar.gz"
	member := "veto_0.1.1_darwin_arm64/veto"
	archive := doctorTestTarArchive(t, member, candidate, tar.TypeReg)
	doctor := newOfficialDoctorTestDeps(t.TempDir(), current, map[string][]byte{
		"BINARY_SHA256SUMS": []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(candidate), member)),
		"SHA256SUMS":        []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), archiveName)),
		archiveName:         archive,
	})
	doctor.validateCandidate = func(string, string) error { return nil }

	require.NoError(t, installUpdate(t.Context(), "0.1.1", updateInstallDeps{doctor: doctor}))
	got, err := os.ReadFile(current)
	require.NoError(t, err)
	assert.Equal(t, candidate, got)
}
