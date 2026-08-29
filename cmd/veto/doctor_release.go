package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	doctorReleaseBaseURL        = "https://github.com/oleg-koval/veto/releases/download"
	doctorManifestMaxBytes      = 1 << 20
	doctorArchiveMaxBytes       = 128 << 20
	doctorBinaryMaxBytes        = 64 << 20
	doctorReleaseRequestTimeout = 20 * time.Second
)

func checkDoctorReleaseIntegrity(options doctorOptions, deps doctorDeps) doctorCheck {
	if options.offline {
		return doctorCheck{ID: "release.integrity", Status: doctorWarn, Message: "offline mode; official checksum verification was skipped"}
	}
	if deps.buildProvenance != "official" {
		return doctorCheck{ID: "release.integrity", Status: doctorWarn, Message: "source/go-install build; official checksum verification is not applicable"}
	}
	resolved := resolveBuildVersion(deps.linkerVersion, deps.readBuildInfo)
	if resolved == "dev" {
		return doctorCheck{ID: "release.integrity", Status: doctorFail, Message: "official build marker is present but the version is unresolved"}
	}
	executablePath, err := deps.executable()
	if err != nil || executablePath == "" {
		return doctorCheck{ID: "release.integrity", Status: doctorFail, Message: "running executable cannot be resolved for checksum verification"}
	}
	binaryAssetPath := doctorBinaryAssetPath(resolved, deps.goos, deps.goarch)
	manifest, err := fetchDoctorReleaseAsset(context.Background(), deps, resolved, "BINARY_SHA256SUMS", doctorManifestMaxBytes)
	if err != nil {
		return doctorCheck{ID: "release.integrity", Status: doctorFail, Message: "official binary checksum manifest is unavailable or untrusted"}
	}
	expected, err := parseDoctorChecksum(manifest, binaryAssetPath)
	if err != nil {
		return doctorCheck{ID: "release.integrity", Status: doctorFail, Message: "official binary checksum manifest is malformed or incomplete"}
	}
	current, err := readDoctorFileLimited(deps.fs, executablePath, doctorBinaryMaxBytes)
	if err != nil {
		return doctorCheck{ID: "release.integrity", Status: doctorFail, Message: "running executable cannot be read for checksum verification"}
	}
	if sha256.Sum256(current) == expected {
		return doctorCheck{ID: "release.integrity", Status: doctorPass, Message: "running executable matches the official SHA-256 manifest"}
	}

	allowed, reason := doctorReplacementAllowed(executablePath, deps.fs)
	check := doctorCheck{ID: "release.integrity", Status: doctorFail, Message: "running executable does not match the official SHA-256 manifest", Repairable: allowed}
	if !options.fix {
		return check
	}
	if !allowed {
		check.Message += "; automatic replacement refused: " + reason
		return check
	}
	staged, repairErr := repairDoctorExecutable(context.Background(), deps, executablePath, resolved, expected, current)
	if repairErr != nil {
		if installed, readErr := readDoctorFileLimited(deps.fs, executablePath, doctorBinaryMaxBytes); readErr == nil && sha256.Sum256(installed) == expected {
			return doctorCheck{ID: "release.integrity", Status: doctorFixed, Message: "installed the checksum-valid official binary; replacement cleanup reported: " + repairErr.Error()}
		}
		check.Repairable = false
		check.Message = "official binary mismatch remains; verified replacement failed: " + repairErr.Error()
		return check
	}
	if staged != "" {
		check.Repairable = false
		check.Message = fmt.Sprintf("official binary mismatch remains; verified Windows replacement staged at %s; close veto and replace %s with that file", staged, executablePath)
		return check
	}
	return doctorCheck{ID: "release.integrity", Status: doctorFixed, Message: "reinstalled the checksum-valid official binary for version " + resolved}
}

func doctorBinaryAssetPath(version, goos, goarch string) string {
	name := "veto"
	if goos == "windows" {
		name += ".exe"
	}
	return fmt.Sprintf("veto_%s_%s_%s/%s", version, goos, goarch, name)
}

func doctorArchiveAssetName(version, goos, goarch string) string {
	extension := ".tar.gz"
	if goos == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("veto_%s_%s_%s%s", version, goos, goarch, extension)
}

func fetchDoctorReleaseAsset(ctx context.Context, deps doctorDeps, version, asset string, limit int64) ([]byte, error) {
	requestURL := fmt.Sprintf("%s/v%s/%s", doctorReleaseBaseURL, version, asset)
	if err := validateDoctorReleaseURL(requestURL); err != nil {
		return nil, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, doctorReleaseRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "veto-doctor/"+version)
	response, err := deps.httpDo(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release asset returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("release asset exceeds %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("release asset exceeds %d bytes", limit)
	}
	return data, nil
}

func validateDoctorReleaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("release URL must be an uncredentialed github.com HTTPS URL")
	}
	if !strings.HasPrefix(parsed.EscapedPath(), "/oleg-koval/veto/releases/download/v") {
		return errors.New("release URL is outside the Veto release path")
	}
	return nil
}

func newDoctorReleaseHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	return &http.Client{
		Transport: transport,
		Timeout:   doctorReleaseRequestTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many release redirects")
			}
			host := strings.ToLower(request.URL.Hostname())
			trustedHost := host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com")
			if request.URL.Scheme != "https" || !trustedHost || request.URL.User != nil {
				return errors.New("release redirect left GitHub-owned HTTPS hosts")
			}
			return nil
		},
	}
}

func parseDoctorChecksum(manifest []byte, target string) ([32]byte, error) {
	var found [32]byte
	matches := 0
	for _, rawLine := range strings.Split(string(manifest), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return found, errors.New("malformed checksum line")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if !doctorSafeArchivePath(name) {
			return found, errors.New("unsafe checksum path")
		}
		digest, err := hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size {
			return found, errors.New("malformed SHA-256 digest")
		}
		if name == target {
			copy(found[:], digest)
			matches++
		}
	}
	if matches != 1 {
		return found, fmt.Errorf("checksum target occurs %d times", matches)
	}
	return found, nil
}

func readDoctorFileLimited(filesystem doctorFilesystem, filePath string, limit int64) ([]byte, error) {
	file, err := filesystem.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}

func repairDoctorExecutable(ctx context.Context, deps doctorDeps, executablePath, version string, binaryChecksum [32]byte, original []byte) (string, error) {
	archiveName := doctorArchiveAssetName(version, deps.goos, deps.goarch)
	archiveManifest, err := fetchDoctorReleaseAsset(ctx, deps, version, "SHA256SUMS", doctorManifestMaxBytes)
	if err != nil {
		return "", errors.New("archive checksum manifest is unavailable or untrusted")
	}
	archiveChecksum, err := parseDoctorChecksum(archiveManifest, archiveName)
	if err != nil {
		return "", errors.New("archive checksum manifest is malformed or incomplete")
	}
	archive, err := fetchDoctorReleaseAsset(ctx, deps, version, archiveName, doctorArchiveMaxBytes)
	if err != nil {
		return "", errors.New("release archive is unavailable or untrusted")
	}
	if sha256.Sum256(archive) != archiveChecksum {
		return "", errors.New("release archive checksum mismatch")
	}
	candidate, err := extractDoctorArchive(archive, archiveName, doctorBinaryAssetPath(version, deps.goos, deps.goarch))
	if err != nil {
		return "", fmt.Errorf("unsafe release archive: %w", err)
	}
	if sha256.Sum256(candidate) != binaryChecksum {
		return "", errors.New("archive binary checksum mismatch")
	}

	dir := filepath.Dir(executablePath)
	tempFile, err := deps.fs.CreateTemp(dir, ".veto-candidate-*")
	if err != nil {
		return "", errors.New("cannot stage replacement beside the executable")
	}
	tempPath := tempFile.Name()
	keepTemp := false
	defer func() {
		_ = tempFile.Close()
		if !keepTemp {
			_ = deps.fs.Remove(tempPath)
		}
	}()
	if err := tempFile.Chmod(0755); err != nil {
		return "", errors.New("cannot set replacement permissions")
	}
	if _, err := tempFile.Write(candidate); err != nil {
		return "", errors.New("cannot write replacement binary")
	}
	if err := tempFile.Sync(); err != nil {
		return "", errors.New("cannot sync replacement binary")
	}
	if err := tempFile.Close(); err != nil {
		return "", errors.New("cannot close replacement binary")
	}
	if err := deps.validateCandidate(tempPath, version); err != nil {
		return "", errors.New("replacement binary reports the wrong version")
	}
	if deps.goos == "windows" {
		stagedPath := executablePath + ".v" + version + ".new"
		if _, err := deps.fs.Lstat(stagedPath); err == nil {
			return "", errors.New("verified Windows staging path already exists")
		}
		if err := deps.fs.Rename(tempPath, stagedPath); err != nil {
			return "", errors.New("cannot preserve verified Windows replacement")
		}
		keepTemp = true
		return stagedPath, nil
	}
	currentInfo, err := deps.fs.Lstat(executablePath)
	if err != nil {
		return "", errors.New("cannot re-inspect executable before replacement")
	}
	if err := atomicReplaceDoctorExecutable(executablePath, tempPath, original, currentInfo.Mode().Perm(), defaultDoctorReplaceOps()); err != nil {
		return "", fmt.Errorf("atomic executable replacement failed: %w", err)
	}
	keepTemp = true
	return "", nil
}

func extractDoctorArchive(archive []byte, archiveName, expectedMember string) ([]byte, error) {
	if !doctorSafeArchivePath(expectedMember) {
		return nil, errors.New("expected member path is unsafe")
	}
	if strings.HasSuffix(archiveName, ".zip") {
		return extractDoctorZip(archive, expectedMember)
	}
	if strings.HasSuffix(archiveName, ".tar.gz") {
		return extractDoctorTarGzip(archive, expectedMember)
	}
	return nil, errors.New("unsupported archive format")
}

func extractDoctorTarGzip(archive []byte, expectedMember string) ([]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	return extractDoctorTarEntries(reader, expectedMember)
}

func extractDoctorTarEntries(reader *tar.Reader, expectedMember string) ([]byte, error) {
	expectedDir := path.Dir(expectedMember)
	var candidate []byte
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !doctorSafeArchivePath(name) {
			return nil, errors.New("archive contains a traversal path")
		}
		if header.Typeflag == tar.TypeDir && name == expectedDir {
			continue
		}
		if header.Typeflag != tar.TypeReg || name != expectedMember || candidate != nil {
			return nil, errors.New("archive contains unexpected or non-regular content")
		}
		if header.Size <= 0 || header.Size > doctorBinaryMaxBytes {
			return nil, errors.New("archive binary size is invalid")
		}
		candidate, err = io.ReadAll(io.LimitReader(reader, doctorBinaryMaxBytes+1))
		if err != nil || int64(len(candidate)) != header.Size {
			return nil, errors.New("archive binary is truncated")
		}
	}
	if len(candidate) == 0 {
		return nil, errors.New("archive does not contain the expected binary")
	}
	return candidate, nil
}

func extractDoctorZip(archive []byte, expectedMember string) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	expectedDir := path.Dir(expectedMember)
	var candidate []byte
	for _, file := range reader.File {
		name := strings.TrimSuffix(file.Name, "/")
		if !doctorSafeArchivePath(name) || file.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("archive contains an unsafe path or symlink")
		}
		if file.FileInfo().IsDir() && name == expectedDir {
			continue
		}
		if file.FileInfo().IsDir() || name != expectedMember || candidate != nil || file.UncompressedSize64 == 0 || file.UncompressedSize64 > doctorBinaryMaxBytes {
			return nil, errors.New("archive contains unexpected content")
		}
		entry, err := file.Open()
		if err != nil {
			return nil, err
		}
		candidate, err = io.ReadAll(io.LimitReader(entry, doctorBinaryMaxBytes+1))
		closeErr := entry.Close()
		if err != nil || closeErr != nil || uint64(len(candidate)) != file.UncompressedSize64 {
			return nil, errors.New("archive binary is truncated")
		}
	}
	if len(candidate) == 0 {
		return nil, errors.New("archive does not contain the expected binary")
	}
	return candidate, nil
}

func doctorSafeArchivePath(name string) bool {
	if name == "" || strings.Contains(name, "\\") || path.IsAbs(name) {
		return false
	}
	clean := path.Clean(name)
	return clean == name && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func doctorReplacementAllowed(executablePath string, filesystem doctorFilesystem) (bool, string) {
	if doctorPackageManagedPath(executablePath) {
		return false, "package-manager path"
	}
	info, err := filesystem.Lstat(executablePath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, "executable is not a regular unmanaged file"
	}
	if owned, supported := doctorFileOwnedByCurrentUser(info); supported && !owned {
		return false, "executable is owned by another user"
	}
	dirInfo, err := filesystem.Stat(filepath.Dir(executablePath))
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode().Perm()&0200 == 0 {
		return false, "executable directory is not writable"
	}
	if owned, supported := doctorFileOwnedByCurrentUser(dirInfo); supported && !owned {
		return false, "executable directory is owned by another user"
	}
	return true, ""
}

func doctorPackageManagedPath(executablePath string) bool {
	clean := strings.ToLower(filepath.ToSlash(filepath.Clean(executablePath)))
	prefixes := []string{
		"/opt/homebrew/",
		"/home/linuxbrew/.linuxbrew/",
		"/usr/local/homebrew/",
		"/nix/store/",
		"/snap/",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(clean, prefix) {
			return true
		}
	}
	return strings.Contains(clean, "/cellar/")
}

func validateDoctorCandidateVersion(candidatePath, wantVersion string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, candidatePath, "version").CombinedOutput()
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(output)) != "veto "+wantVersion {
		return errors.New("candidate version mismatch")
	}
	return nil
}

type doctorReplaceOps struct {
	createTemp func(string, string) (*os.File, error)
	rename     func(string, string) error
	remove     func(string) error
	syncDir    func(string) error
}

func defaultDoctorReplaceOps() doctorReplaceOps {
	return doctorReplaceOps{
		createTemp: os.CreateTemp,
		rename:     os.Rename,
		remove:     os.Remove,
		syncDir: func(dir string) error {
			file, err := os.Open(dir)
			if err != nil {
				return err
			}
			defer file.Close()
			return file.Sync()
		},
	}
}

func atomicReplaceDoctorExecutable(currentPath, candidatePath string, original []byte, mode os.FileMode, ops doctorReplaceOps) error {
	dir := filepath.Dir(currentPath)
	backup, err := ops.createTemp(dir, ".veto-rollback-*")
	if err != nil {
		return err
	}
	backupPath := backup.Name()
	backupPresent := true
	defer func() {
		_ = backup.Close()
		if backupPresent {
			_ = ops.remove(backupPath)
		}
	}()
	if err := backup.Chmod(mode); err != nil {
		return err
	}
	if _, err := backup.Write(original); err != nil {
		return err
	}
	if err := backup.Sync(); err != nil {
		return err
	}
	if err := backup.Close(); err != nil {
		return err
	}
	if err := ops.rename(candidatePath, currentPath); err != nil {
		return err
	}
	if err := ops.syncDir(dir); err != nil {
		rollbackErr := ops.rename(backupPath, currentPath)
		if rollbackErr == nil {
			backupPresent = false
			_ = ops.syncDir(dir)
			return fmt.Errorf("directory sync failed; original restored: %w", err)
		}
		return fmt.Errorf("directory sync failed and rollback failed: %v; rollback: %w", err, rollbackErr)
	}
	if err := ops.remove(backupPath); err != nil {
		return err
	}
	backupPresent = false
	return ops.syncDir(dir)
}
