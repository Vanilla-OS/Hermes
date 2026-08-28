package downloader

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vanilla-os/Hermes/pkg/release"
	"github.com/vanilla-os/Hermes/pkg/utils"
)

const (
	maxArchiveSize  = int64(8 << 30)
	maxChecksumSize = int64(1 << 20)
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	isoPattern        = regexp.MustCompile(`^Vanilla-OS-.+-(amd64|arm64)\.(\d{8})\.iso$`)
	archPatterns      = map[string]*regexp.Regexp{
		"amd64": regexp.MustCompile(`(?i)(^|[^a-z0-9])amd64([^a-z0-9]|$)`),
		"arm64": regexp.MustCompile(`(?i)(^|[^a-z0-9])arm64([^a-z0-9]|$)`),
	}
	requiredArchitectures = []string{"amd64", "arm64"}
)

type Config struct {
	Repository string
	Root       string
	PublicURL  string
	NightlyURL string
	Keep       int
}

type Syncer struct {
	config Config
	client *http.Client
}

type build struct {
	Arch         string
	Date         string
	ISOName      string
	ChecksumName string
	ISOPath      string
	ChecksumPath string
}

type state struct {
	RunID   int64             `json:"run_id"`
	Files   map[string]string `json:"files"`
	Managed []string          `json:"managed,omitempty"`
}

type download struct {
	Arch   string `json:"Arch"`
	Date   string `json:"Date"`
	Iso    string `json:"Iso"`
	Sha256 string `json:"Sha256"`
}

func New(config Config, client *http.Client) (*Syncer, error) {
	if !repositoryPattern.MatchString(config.Repository) {
		return nil, fmt.Errorf("invalid repository %q", config.Repository)
	}
	if config.Root == "" {
		return nil, errors.New("root is required")
	}
	if err := validateBaseURL(config.PublicURL); err != nil {
		return nil, fmt.Errorf("invalid public URL: %w", err)
	}
	if err := validateBaseURL(config.NightlyURL); err != nil {
		return nil, fmt.Errorf("invalid nightly URL: %w", err)
	}
	if config.Keep < 1 {
		return nil, errors.New("keep must be at least 1")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Syncer{config: config, client: client}, nil
}

func (s *Syncer) Sync(ctx context.Context, current release.Release) (bool, error) {
	if err := utils.CreateDir(s.config.Root); err != nil {
		return false, err
	}

	previous, err := s.readState()
	if err != nil {
		return false, err
	}
	complete, err := s.isComplete(current.ID, previous)
	if err != nil {
		return false, err
	}
	if complete {
		return false, nil
	}

	artifacts, err := selectArtifacts(current.Artifacts)
	if err != nil {
		return false, err
	}

	staging, err := os.MkdirTemp(s.config.Root, ".hermes-stage-*")
	if err != nil {
		return false, fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(staging)

	builds := make([]build, 0, len(requiredArchitectures))
	for _, arch := range requiredArchitectures {
		artifact := artifacts[arch]
		archivePath := filepath.Join(staging, arch+".zip")
		if err := s.downloadArtifact(ctx, current.ID, artifact.Name, archivePath); err != nil {
			return false, fmt.Errorf("download %s artifact: %w", arch, err)
		}
		candidate, err := extractBuild(archivePath, staging, arch)
		if err != nil {
			return false, fmt.Errorf("extract %s artifact: %w", arch, err)
		}
		if err := verifyBuild(candidate); err != nil {
			return false, fmt.Errorf("verify %s artifact: %w", arch, err)
		}
		builds = append(builds, candidate)
	}

	for _, candidate := range builds {
		if err := publishBuild(s.config.Root, candidate); err != nil {
			return false, err
		}
	}
	for _, candidate := range builds {
		if err := utils.ReplaceSymlink(candidate.ISOName, filepath.Join(s.config.Root, "latest-"+candidate.Arch+".iso")); err != nil {
			return false, fmt.Errorf("update %s ISO link: %w", candidate.Arch, err)
		}
		if err := utils.ReplaceSymlink(candidate.ChecksumName, filepath.Join(s.config.Root, "latest-"+candidate.Arch+".sha256.txt")); err != nil {
			return false, fmt.Errorf("update %s checksum link: %w", candidate.Arch, err)
		}
	}

	if err := s.writeManifest(builds); err != nil {
		return false, err
	}
	managed := previous.managedFiles()
	for _, candidate := range builds {
		managed = append(managed, candidate.ISOName, candidate.ChecksumName)
	}
	managed, err = cleanupBuilds(s.config.Root, s.config.Keep, managed)
	if err != nil {
		return false, err
	}

	files := make(map[string]string, len(builds)*2)
	for _, candidate := range builds {
		files[candidate.Arch+"_iso"] = candidate.ISOName
		files[candidate.Arch+"_sha256"] = candidate.ChecksumName
	}
	if err := utils.WriteJSON(filepath.Join(s.config.Root, ".hermes-state.json"), state{RunID: current.ID, Files: files, Managed: managed}); err != nil {
		return false, fmt.Errorf("write state: %w", err)
	}
	return true, nil
}

func selectArtifacts(artifacts []release.Artifact) (map[string]release.Artifact, error) {
	selected := make(map[string]release.Artifact, len(requiredArchitectures))
	for _, artifact := range artifacts {
		if artifact.Expired {
			continue
		}
		for _, arch := range requiredArchitectures {
			if !archPatterns[arch].MatchString(artifact.Name) {
				continue
			}
			if _, exists := selected[arch]; exists {
				return nil, fmt.Errorf("multiple %s artifacts found", arch)
			}
			selected[arch] = artifact
		}
	}
	for _, arch := range requiredArchitectures {
		if _, exists := selected[arch]; !exists {
			return nil, fmt.Errorf("%s artifact not found", arch)
		}
	}
	return selected, nil
}

func (s *Syncer) downloadArtifact(ctx context.Context, runID int64, artifactName, destination string) error {
	artifactURL := fmt.Sprintf("%s/%s/actions/runs/%d/%s.zip", strings.TrimRight(s.config.NightlyURL, "/"), s.config.Repository, runID, url.PathEscape(artifactName))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}
	if resp.ContentLength > maxArchiveSize {
		return fmt.Errorf("archive is larger than %d bytes", maxArchiveSize)
	}

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	written, copyErr := io.Copy(output, io.LimitReader(resp.Body, maxArchiveSize+1))
	closeErr := output.Close()
	if copyErr != nil {
		return fmt.Errorf("write archive: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close archive: %w", closeErr)
	}
	if written > maxArchiveSize {
		return fmt.Errorf("archive is larger than %d bytes", maxArchiveSize)
	}
	return nil
}

func extractBuild(archivePath, staging, expectedArch string) (build, error) {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return build{}, fmt.Errorf("open ZIP archive: %w", err)
	}
	defer archive.Close()

	var candidate build
	for _, entry := range archive.File {
		cleanName := filepath.ToSlash(filepath.Clean(entry.Name))
		if strings.HasPrefix(cleanName, "../") || strings.HasPrefix(cleanName, "/") {
			return build{}, fmt.Errorf("unsafe ZIP path %q", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if !entry.Mode().IsRegular() {
			return build{}, fmt.Errorf("unsupported ZIP entry %q", entry.Name)
		}

		name := filepath.Base(cleanName)
		isoMatch := isoPattern.FindStringSubmatch(name)
		isChecksum := strings.HasSuffix(name, ".sha256.txt")
		if isoMatch == nil && !isChecksum {
			continue
		}

		isoName := name
		if isChecksum {
			isoName = strings.TrimSuffix(name, ".sha256.txt") + ".iso"
			isoMatch = isoPattern.FindStringSubmatch(isoName)
			if isoMatch == nil {
				continue
			}
		}
		if isoMatch[1] != expectedArch {
			return build{}, fmt.Errorf("artifact contains %s file %q", isoMatch[1], name)
		}

		if candidate.Arch == "" {
			candidate.Arch = isoMatch[1]
			candidate.Date = isoMatch[2]
			candidate.ISOName = isoName
			candidate.ChecksumName = strings.TrimSuffix(isoName, ".iso") + ".sha256.txt"
		} else if candidate.ISOName != isoName {
			return build{}, fmt.Errorf("artifact contains multiple ISO builds")
		}

		if isChecksum {
			if candidate.ChecksumPath != "" {
				return build{}, fmt.Errorf("artifact contains multiple checksum files")
			}
			candidate.ChecksumPath = filepath.Join(staging, expectedArch+".sha256.txt")
			if err := extractEntry(entry, candidate.ChecksumPath, maxChecksumSize); err != nil {
				return build{}, err
			}
		} else {
			if candidate.ISOPath != "" {
				return build{}, fmt.Errorf("artifact contains multiple ISO files")
			}
			candidate.ISOPath = filepath.Join(staging, expectedArch+".iso")
			if err := extractEntry(entry, candidate.ISOPath, maxArchiveSize); err != nil {
				return build{}, err
			}
		}
	}

	if candidate.ISOPath == "" || candidate.ChecksumPath == "" {
		return build{}, errors.New("ISO or SHA256 file not found")
	}
	return candidate, nil
}

func extractEntry(entry *zip.File, destination string, maximum int64) error {
	if int64(entry.UncompressedSize64) > maximum {
		return fmt.Errorf("ZIP entry %q is too large", entry.Name)
	}
	input, err := entry.Open()
	if err != nil {
		return fmt.Errorf("open ZIP entry %q: %w", entry.Name, err)
	}
	defer input.Close()

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create extracted file: %w", err)
	}
	written, copyErr := io.Copy(output, io.LimitReader(input, maximum+1))
	closeErr := output.Close()
	if copyErr != nil {
		return fmt.Errorf("extract ZIP entry %q: %w", entry.Name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close extracted file: %w", closeErr)
	}
	if written > maximum {
		return fmt.Errorf("ZIP entry %q is too large", entry.Name)
	}
	return nil
}

func verifyBuild(candidate build) error {
	checksumFile, err := os.Open(candidate.ChecksumPath)
	if err != nil {
		return fmt.Errorf("open checksum: %w", err)
	}
	scanner := bufio.NewScanner(checksumFile)
	if !scanner.Scan() {
		checksumFile.Close()
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read checksum: %w", err)
		}
		return errors.New("checksum file is empty")
	}
	fields := strings.Fields(scanner.Text())
	if err := checksumFile.Close(); err != nil {
		return fmt.Errorf("close checksum: %w", err)
	}
	if len(fields) != 2 {
		return errors.New("invalid checksum format")
	}
	expected, err := hex.DecodeString(fields[0])
	if err != nil || len(expected) != sha256.Size {
		return errors.New("invalid SHA256 digest")
	}
	checksumTarget := strings.TrimPrefix(filepath.Base(fields[1]), "*")
	if checksumTarget != candidate.ISOName {
		return fmt.Errorf("checksum references %q instead of %q", checksumTarget, candidate.ISOName)
	}

	iso, err := os.Open(candidate.ISOPath)
	if err != nil {
		return fmt.Errorf("open ISO: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, iso); err != nil {
		iso.Close()
		return fmt.Errorf("hash ISO: %w", err)
	}
	if err := iso.Close(); err != nil {
		return fmt.Errorf("close ISO: %w", err)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), fields[0]) {
		return errors.New("ISO checksum does not match")
	}
	return nil
}

func publishBuild(root string, candidate build) error {
	checksumDestination := filepath.Join(root, candidate.ChecksumName)
	if err := os.Rename(candidate.ChecksumPath, checksumDestination); err != nil {
		return fmt.Errorf("publish %s checksum: %w", candidate.Arch, err)
	}
	if err := os.Chmod(checksumDestination, 0o644); err != nil {
		return fmt.Errorf("set %s checksum permissions: %w", candidate.Arch, err)
	}
	isoDestination := filepath.Join(root, candidate.ISOName)
	if err := os.Rename(candidate.ISOPath, isoDestination); err != nil {
		return fmt.Errorf("publish %s ISO: %w", candidate.Arch, err)
	}
	if err := os.Chmod(isoDestination, 0o644); err != nil {
		return fmt.Errorf("set %s ISO permissions: %w", candidate.Arch, err)
	}
	return nil
}

func (s *Syncer) writeManifest(builds []build) error {
	manifest := make([]download, 0, len(builds))
	for _, candidate := range builds {
		manifest = append(manifest, download{
			Arch:   candidate.Arch,
			Date:   candidate.Date[0:4] + "-" + candidate.Date[4:6] + "-" + candidate.Date[6:8],
			Iso:    strings.TrimRight(s.config.PublicURL, "/") + "/" + url.PathEscape(candidate.ISOName),
			Sha256: strings.TrimRight(s.config.PublicURL, "/") + "/" + url.PathEscape(candidate.ChecksumName),
		})
	}
	if err := utils.WriteJSON(filepath.Join(s.config.Root, "downloads.json"), manifest); err != nil {
		return fmt.Errorf("write downloads manifest: %w", err)
	}
	return nil
}

func cleanupBuilds(root string, keep int, managed []string) ([]string, error) {
	managedSet := make(map[string]bool, len(managed))
	for _, name := range managed {
		managedSet[name] = true
	}

	for _, arch := range requiredArchitectures {
		var names []string
		for name := range managedSet {
			match := isoPattern.FindStringSubmatch(name)
			if match != nil && match[1] == arch {
				names = append(names, name)
			}
		}
		sort.Slice(names, func(i, j int) bool {
			left := isoPattern.FindStringSubmatch(names[i])[2]
			right := isoPattern.FindStringSubmatch(names[j])[2]
			if left == right {
				return names[i] > names[j]
			}
			return left > right
		})
		if len(names) <= keep {
			continue
		}
		for _, name := range names[keep:] {
			if err := os.Remove(filepath.Join(root, name)); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("remove old ISO %s: %w", name, err)
			}
			delete(managedSet, name)
			checksumName := strings.TrimSuffix(name, ".iso") + ".sha256.txt"
			if managedSet[checksumName] {
				if err := os.Remove(filepath.Join(root, checksumName)); err != nil && !os.IsNotExist(err) {
					return nil, fmt.Errorf("remove old checksum %s: %w", checksumName, err)
				}
				delete(managedSet, checksumName)
			}
		}
	}

	retained := make([]string, 0, len(managedSet))
	for name := range managedSet {
		retained = append(retained, name)
	}
	sort.Strings(retained)
	return retained, nil
}

func (s *Syncer) readState() (state, error) {
	statePath := filepath.Join(s.config.Root, ".hermes-state.json")
	stateFile, err := os.Open(statePath)
	if os.IsNotExist(err) {
		return state{}, nil
	}
	if err != nil {
		return state{}, fmt.Errorf("open state: %w", err)
	}
	defer stateFile.Close()

	var current state
	if err := json.NewDecoder(stateFile).Decode(&current); err != nil {
		return state{}, fmt.Errorf("decode state: %w", err)
	}
	return current, nil
}

func (s *Syncer) isComplete(runID int64, current state) (bool, error) {
	if current.RunID != runID {
		return false, nil
	}
	for _, arch := range requiredArchitectures {
		isoName := current.Files[arch+"_iso"]
		checksumName := current.Files[arch+"_sha256"]
		if isoName == "" || checksumName == "" {
			return false, nil
		}
		for _, name := range []string{isoName, checksumName} {
			info, err := os.Stat(filepath.Join(s.config.Root, name))
			if os.IsNotExist(err) {
				return false, nil
			}
			if err != nil {
				return false, fmt.Errorf("check published file %s: %w", name, err)
			}
			if !info.Mode().IsRegular() {
				return false, nil
			}
		}
		links := map[string]string{
			"latest-" + arch + ".iso":        isoName,
			"latest-" + arch + ".sha256.txt": checksumName,
		}
		for link, target := range links {
			actual, err := os.Readlink(filepath.Join(s.config.Root, link))
			if err != nil || actual != target {
				return false, nil
			}
		}
	}
	if _, err := os.Stat(filepath.Join(s.config.Root, "downloads.json")); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("check downloads manifest: %w", err)
	}
	return true, nil
}

func (s state) managedFiles() []string {
	if len(s.Managed) > 0 {
		return append([]string(nil), s.Managed...)
	}
	managed := make([]string, 0, len(s.Files))
	for _, name := range s.Files {
		if name != "" {
			managed = append(managed, name)
		}
	}
	return managed
}

func validateBaseURL(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil {
		return err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("URL must use HTTP or HTTPS and include a host")
	}
	return nil
}
