package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorReleaseIntegrityMatchesOfficialBinary(t *testing.T) {
	home := t.TempDir()
	current := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	deps := newOfficialDoctorTestDeps(home, current, map[string][]byte{
		"BINARY_SHA256SUMS": []byte(fmt.Sprintf("%x  veto_0.1.0_darwin_arm64/veto\n", sha256.Sum256([]byte("test binary")))),
	})

	report := runDoctor(doctorOptions{}, deps)
	assert.True(t, report.OK, report.Checks)
	assertDoctorCheck(t, report, "release.integrity", doctorPass)
}

func TestDoctorReleaseIntegrityFailsClosedWhenManifestMissing(t *testing.T) {
	home := t.TempDir()
	current := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	deps := newOfficialDoctorTestDeps(home, current, map[string][]byte{})

	report := runDoctor(doctorOptions{}, deps)
	assert.False(t, report.OK)
	assertDoctorCheck(t, report, "release.integrity", doctorFail)
}

func TestDoctorOfficialBuildRejectsMalformedVersionWithoutNetwork(t *testing.T) {
	home := t.TempDir()
	current := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	deps := newOfficialDoctorTestDeps(home, current, nil)
	deps.linkerVersion = "../../latest"
	requests := 0
	deps.httpDo = func(*http.Request) (*http.Response, error) {
		requests++
		return nil, assert.AnError
	}

	report := runDoctor(doctorOptions{}, deps)
	assert.False(t, report.OK)
	assert.Zero(t, requests)
	assertDoctorCheck(t, report, "release.integrity", doctorFail)
}

func TestDoctorFixReinstallsChecksumValidSameVersionArchive(t *testing.T) {
	home := t.TempDir()
	current := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	candidate := []byte("#!/bin/sh\necho 'veto 0.1.0'\n")
	archiveName := "veto_0.1.0_darwin_arm64.tar.gz"
	member := "veto_0.1.0_darwin_arm64/veto"
	archive := doctorTestTarArchive(t, member, candidate, tar.TypeReg)
	deps := newOfficialDoctorTestDeps(home, current, map[string][]byte{
		"BINARY_SHA256SUMS": []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(candidate), member)),
		"SHA256SUMS":        []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), archiveName)),
		archiveName:         archive,
	})

	report := runDoctor(doctorOptions{fix: true}, deps)
	assert.True(t, report.OK, report.Checks)
	assertDoctorCheck(t, report, "release.integrity", doctorFixed)
	got, err := os.ReadFile(current)
	require.NoError(t, err)
	assert.Equal(t, candidate, got)
}

func TestDoctorFixPreservesExecutablePermissions(t *testing.T) {
	home := t.TempDir()
	current := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	require.NoError(t, os.Chmod(current, 0700))
	candidate := []byte("veto 0.1.0 candidate")
	archiveName := "veto_0.1.0_darwin_arm64.tar.gz"
	member := "veto_0.1.0_darwin_arm64/veto"
	archive := doctorTestTarArchive(t, member, candidate, tar.TypeReg)
	deps := newOfficialDoctorTestDeps(home, current, map[string][]byte{
		"BINARY_SHA256SUMS": []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(candidate), member)),
		"SHA256SUMS":        []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), archiveName)),
		archiveName:         archive,
	})

	report := runDoctor(doctorOptions{fix: true}, deps)
	assert.True(t, report.OK, report.Checks)
	info, err := os.Stat(current)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), info.Mode().Perm())
}

func TestDoctorFixRejectsHostileOrChecksumInvalidArchives(t *testing.T) {
	candidate := []byte("#!/bin/sh\necho 'veto 0.1.0'\n")
	archiveName := "veto_0.1.0_darwin_arm64.tar.gz"
	member := "veto_0.1.0_darwin_arm64/veto"
	tests := []struct {
		name    string
		archive []byte
		sum     [32]byte
	}{
		{name: "traversal", archive: doctorTestTarArchive(t, "../veto", candidate, tar.TypeReg)},
		{name: "symlink", archive: doctorTestTarArchive(t, member, []byte("target"), tar.TypeSymlink)},
		{name: "bad archive checksum", archive: doctorTestTarArchive(t, member, candidate, tar.TypeReg), sum: sha256.Sum256([]byte("other"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			current := writeDoctorTestExecutable(t, t.TempDir(), "veto")
			archiveSum := test.sum
			if archiveSum == ([32]byte{}) {
				archiveSum = sha256.Sum256(test.archive)
			}
			deps := newOfficialDoctorTestDeps(home, current, map[string][]byte{
				"BINARY_SHA256SUMS": []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(candidate), member)),
				"SHA256SUMS":        []byte(fmt.Sprintf("%x  %s\n", archiveSum, archiveName)),
				archiveName:         test.archive,
			})

			report := runDoctor(doctorOptions{fix: true}, deps)
			assert.False(t, report.OK)
			assertDoctorCheck(t, report, "release.integrity", doctorFail)
			got, err := os.ReadFile(current)
			require.NoError(t, err)
			assert.Equal(t, []byte("test binary"), got)
		})
	}
}

func TestDoctorFixRejectsCandidateWithWrongReportedVersion(t *testing.T) {
	home := t.TempDir()
	current := writeDoctorTestExecutable(t, t.TempDir(), "veto")
	candidate := []byte("candidate bytes")
	archiveName := "veto_0.1.0_darwin_arm64.tar.gz"
	member := "veto_0.1.0_darwin_arm64/veto"
	archive := doctorTestTarArchive(t, member, candidate, tar.TypeReg)
	deps := newOfficialDoctorTestDeps(home, current, map[string][]byte{
		"BINARY_SHA256SUMS": []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(candidate), member)),
		"SHA256SUMS":        []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), archiveName)),
		archiveName:         archive,
	})
	deps.validateCandidate = func(string, string) error { return assert.AnError }

	report := runDoctor(doctorOptions{fix: true}, deps)
	assert.False(t, report.OK)
	assertDoctorCheck(t, report, "release.integrity", doctorFail)
	got, err := os.ReadFile(current)
	require.NoError(t, err)
	assert.Equal(t, []byte("test binary"), got)
}

func TestDoctorFixStagesWindowsReplacementAndLeavesFailureUnresolved(t *testing.T) {
	home := t.TempDir()
	current := writeDoctorTestExecutable(t, t.TempDir(), "veto.exe")
	candidate := []byte("veto 0.1.0 windows candidate")
	archiveName := "veto_0.1.0_windows_amd64.zip"
	member := "veto_0.1.0_windows_amd64/veto.exe"
	archive := doctorTestZipArchive(t, member, candidate)
	deps := newOfficialDoctorTestDeps(home, current, map[string][]byte{
		"BINARY_SHA256SUMS": []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(candidate), member)),
		"SHA256SUMS":        []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), archiveName)),
		archiveName:         archive,
	})
	deps.goos = "windows"
	deps.goarch = "amd64"

	report := runDoctor(doctorOptions{fix: true}, deps)
	assert.False(t, report.OK)
	check := findDoctorCheck(t, report, "release.integrity")
	assert.Equal(t, doctorFail, check.Status)
	assert.Contains(t, check.Message, "close veto and replace")
	staged, err := filepath.Glob(filepath.Join(filepath.Dir(current), ".veto-candidate-*"))
	require.NoError(t, err)
	require.Len(t, staged, 1)
	got, err := os.ReadFile(staged[0])
	require.NoError(t, err)
	assert.Equal(t, candidate, got)
}

func TestExtractDoctorArchiveAcceptsOnlyExpectedBinary(t *testing.T) {
	candidate := []byte("binary")
	tarArchive := doctorTestTarArchive(t, "veto_0.1.0_linux_amd64/veto", candidate, tar.TypeReg)
	got, err := extractDoctorArchive(tarArchive, "veto_0.1.0_linux_amd64.tar.gz", "veto_0.1.0_linux_amd64/veto")
	require.NoError(t, err)
	assert.Equal(t, candidate, got)

	zipArchive := doctorTestZipArchive(t, "veto_0.1.0_windows_amd64/veto.exe", candidate)
	got, err = extractDoctorArchive(zipArchive, "veto_0.1.0_windows_amd64.zip", "veto_0.1.0_windows_amd64/veto.exe")
	require.NoError(t, err)
	assert.Equal(t, candidate, got)

	extra := doctorTestZipArchive(t, "other.txt", []byte("unexpected"))
	_, err = extractDoctorArchive(extra, "veto_0.1.0_windows_amd64.zip", "veto_0.1.0_windows_amd64/veto.exe")
	assert.Error(t, err)
}

func TestParseDoctorChecksumRejectsMissingDuplicateAndMalformedEntries(t *testing.T) {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("binary")))
	target := "veto_0.1.0_linux_amd64/veto"
	_, err := parseDoctorChecksum([]byte(digest+"  other\n"), target)
	assert.Error(t, err)
	_, err = parseDoctorChecksum([]byte(digest+"  "+target+"\n"+digest+"  "+target+"\n"), target)
	assert.Error(t, err)
	_, err = parseDoctorChecksum([]byte("not-a-digest  "+target+"\n"), target)
	assert.Error(t, err)
}

func TestDoctorOfflineAndSourceBuildNeverRequestReleaseAssets(t *testing.T) {
	for _, test := range []struct {
		name       string
		options    doctorOptions
		provenance string
	}{
		{name: "offline", options: doctorOptions{offline: true}, provenance: "official"},
		{name: "source", options: doctorOptions{}, provenance: "source"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			current := writeDoctorTestExecutable(t, t.TempDir(), "veto")
			deps := newOfficialDoctorTestDeps(home, current, nil)
			deps.buildProvenance = test.provenance
			requests := 0
			deps.httpDo = func(*http.Request) (*http.Response, error) {
				requests++
				return nil, assert.AnError
			}
			report := runDoctor(test.options, deps)
			assert.True(t, report.OK, report.Checks)
			assert.Zero(t, requests)
			assertDoctorCheck(t, report, "release.integrity", doctorWarn)
		})
	}
}

func TestDoctorReleaseHTTPClientRejectsUntrustedRedirects(t *testing.T) {
	client := newDoctorReleaseHTTPClient()
	request := &http.Request{URL: &url.URL{Scheme: "https", Host: "evil.example", Path: "/payload"}}
	via := []*http.Request{{URL: &url.URL{Scheme: "https", Host: "github.com", Path: "/oleg-koval/veto/releases/download/v0.1.0/file"}}}
	assert.Error(t, client.CheckRedirect(request, via))

	request.URL.Host = "release-assets.githubusercontent.com"
	assert.NoError(t, client.CheckRedirect(request, via))
	request.URL.Scheme = "http"
	assert.Error(t, client.CheckRedirect(request, via))
}

func TestDoctorReplacementRollsBackAfterPostRenameFailure(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "veto")
	candidate := filepath.Join(dir, ".veto-candidate")
	require.NoError(t, os.WriteFile(current, []byte("original"), 0755))
	require.NoError(t, os.WriteFile(candidate, []byte("replacement"), 0755))
	syncCalls := 0
	ops := defaultDoctorReplaceOps()
	ops.syncDir = func(string) error {
		syncCalls++
		if syncCalls == 1 {
			return assert.AnError
		}
		return nil
	}

	err := atomicReplaceDoctorExecutable(current, candidate, []byte("original"), 0755, ops)
	require.Error(t, err)
	got, readErr := os.ReadFile(current)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("original"), got)
}

func TestDoctorRefusesManagedAndUnwritableReplacementTargets(t *testing.T) {
	assert.True(t, doctorPackageManagedPath("/opt/homebrew/bin/veto"))
	assert.True(t, doctorPackageManagedPath("/usr/local/Cellar/veto/0.1.0/bin/veto"))
	assert.True(t, doctorPackageManagedPath(`C:\Users\test\scoop\apps\veto\current\veto.exe`))
	assert.True(t, doctorPackageManagedPath(`C:\ProgramData\chocolatey\bin\veto.exe`))
	assert.False(t, doctorPackageManagedPath("/Users/test/.local/bin/veto"))

	dir := t.TempDir()
	current := writeDoctorTestExecutable(t, dir, "veto")
	require.NoError(t, os.Chmod(dir, 0500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	allowed, _ := doctorReplacementAllowed(current, osDoctorFilesystem{})
	assert.False(t, allowed)
	require.NoError(t, os.Chmod(dir, 0722))
	allowed, _ = doctorReplacementAllowed(current, osDoctorFilesystem{})
	assert.False(t, allowed)
	if doctorStatePermissionsSupported() {
		require.NoError(t, os.Chmod(dir, os.ModeSticky|0700))
		allowed, _ = doctorReplacementAllowed(current, osDoctorFilesystem{})
		assert.False(t, allowed)
	}
}

func newOfficialDoctorTestDeps(home, current string, assets map[string][]byte) doctorDeps {
	deps := newDoctorTestDeps(home, current)
	deps.linkerVersion = "0.1.0"
	deps.buildProvenance = "official"
	deps.goos = "darwin"
	deps.goarch = "arm64"
	deps.httpDo = func(request *http.Request) (*http.Response, error) {
		name := filepath.Base(request.URL.Path)
		body, ok := assets[name]
		if !ok {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("missing")), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	}
	deps.validateCandidate = func(path, wantVersion string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Contains(data, []byte("veto "+wantVersion)) {
			return fmt.Errorf("candidate version mismatch")
		}
		return nil
	}
	return deps
}

func findDoctorCheck(t *testing.T, report doctorReport, id string) doctorCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("doctor check %q not found", id)
	return doctorCheck{}
}

func doctorTestTarArchive(t *testing.T, name string, contents []byte, typeFlag byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	header := &tar.Header{Name: name, Mode: 0755, Size: int64(len(contents)), Typeflag: typeFlag}
	if typeFlag == tar.TypeSymlink {
		header.Linkname = string(contents)
		header.Size = 0
	}
	require.NoError(t, tarWriter.WriteHeader(header))
	if typeFlag == tar.TypeReg {
		_, err := tarWriter.Write(contents)
		require.NoError(t, err)
	}
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
	return compressed.Bytes()
}

func doctorTestZipArchive(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create(name)
	require.NoError(t, err)
	_, err = entry.Write(contents)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return archive.Bytes()
}
