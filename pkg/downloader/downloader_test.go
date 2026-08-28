package downloader

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vanilla-os/Hermes/pkg/release"
)

func TestSyncPublishesBothArchitectures(t *testing.T) {
	root := t.TempDir()
	unknownPath := filepath.Join(root, "index.html")
	if err := os.WriteFile(unknownPath, []byte("download page"), 0o644); err != nil {
		t.Fatal(err)
	}
	preexisting := make(map[string]string, len(requiredArchitectures)*2)
	for _, arch := range requiredArchitectures {
		iso := isoName(arch, "20260812")
		checksum := strings.TrimSuffix(iso, ".iso") + ".sha256.txt"
		preexisting[iso] = "existing ISO"
		preexisting[checksum] = "existing checksum"
	}
	for name, content := range preexisting {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		arch := architectureFromPath(t, r.URL.Path)
		date := "20260826"
		if strings.Contains(r.URL.Path, "/43/") {
			date = "20260827"
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(buildArchive(t, arch, date, false))
	}))
	defer server.Close()

	syncer, err := New(Config{
		Repository: "Vanilla-OS/live-iso",
		Root:       root,
		PublicURL:  "https://download.vanillaos.org",
		NightlyURL: server.URL,
		Keep:       1,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	updated, err := syncer.Sync(context.Background(), testRelease(42, "2026-08-26"))
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("expected the first synchronization to publish files")
	}
	assertPublished(t, root, "amd64", "20260826")
	assertPublished(t, root, "arm64", "20260826")
	assertManifest(t, root, "2026-08-26")

	updated, err = syncer.Sync(context.Background(), testRelease(42, "2026-08-26"))
	if err != nil {
		t.Fatal(err)
	}
	if updated || requests.Load() != 2 {
		t.Fatalf("unchanged run downloaded again, updated=%v requests=%d", updated, requests.Load())
	}
	legacyState := state{
		RunID: 42,
		Files: map[string]string{
			"amd64_iso":    isoName("amd64", "20260826"),
			"amd64_sha256": strings.TrimSuffix(isoName("amd64", "20260826"), ".iso") + ".sha256.txt",
			"arm64_iso":    isoName("arm64", "20260826"),
			"arm64_sha256": strings.TrimSuffix(isoName("arm64", "20260826"), ".iso") + ".sha256.txt",
		},
	}
	encodedState, err := json.Marshal(legacyState)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hermes-state.json"), encodedState, 0o644); err != nil {
		t.Fatal(err)
	}

	updated, err = syncer.Sync(context.Background(), testRelease(43, "2026-08-27"))
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("expected the new run to publish files")
	}
	assertPublished(t, root, "amd64", "20260827")
	assertPublished(t, root, "arm64", "20260827")
	for _, arch := range requiredArchitectures {
		oldName := isoName(arch, "20260826")
		if _, err := os.Stat(filepath.Join(root, oldName)); !os.IsNotExist(err) {
			t.Fatalf("old build %s was not removed", oldName)
		}
	}
	if content, err := os.ReadFile(unknownPath); err != nil || string(content) != "download page" {
		t.Fatalf("unrecognized file was changed: content=%q err=%v", content, err)
	}
	for name, expected := range preexisting {
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(content) != expected {
			t.Fatalf("preexisting file %s was changed: content=%q err=%v", name, content, err)
		}
	}
}

func TestSyncRejectsChecksumMismatchBeforePublication(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arch := architectureFromPath(t, r.URL.Path)
		_, _ = w.Write(buildArchive(t, arch, "20260826", arch == "arm64"))
	}))
	defer server.Close()

	syncer, err := New(Config{
		Repository: "Vanilla-OS/live-iso",
		Root:       root,
		PublicURL:  "https://download.vanillaos.org",
		NightlyURL: server.URL,
		Keep:       2,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := syncer.Sync(context.Background(), testRelease(42, "2026-08-26")); err == nil {
		t.Fatal("expected checksum verification to fail")
	}
	for _, arch := range requiredArchitectures {
		if _, err := os.Stat(filepath.Join(root, isoName(arch, "20260826"))); !os.IsNotExist(err) {
			t.Fatalf("%s was published after verification failed", arch)
		}
	}
}

func TestSyncRequiresBothArchitectures(t *testing.T) {
	syncer, err := New(Config{
		Repository: "Vanilla-OS/live-iso",
		Root:       t.TempDir(),
		PublicURL:  "https://download.vanillaos.org",
		NightlyURL: "https://nightly.link",
		Keep:       2,
	}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}

	current := testRelease(42, "2026-08-26")
	current.Artifacts = current.Artifacts[:1]
	if _, err := syncer.Sync(context.Background(), current); err == nil || !strings.Contains(err.Error(), "arm64 artifact not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSelectArtifactsRejectsDuplicateAndExpiredArtifacts(t *testing.T) {
	tests := []struct {
		name      string
		artifacts []release.Artifact
		expected  string
	}{
		{
			name: "duplicate",
			artifacts: []release.Artifact{
				{Name: "Vanilla OS AMD64 first"},
				{Name: "Vanilla OS AMD64 second"},
				{Name: "Vanilla OS ARM64"},
			},
			expected: "multiple amd64 artifacts found",
		},
		{
			name: "expired",
			artifacts: []release.Artifact{
				{Name: "Vanilla OS AMD64", Expired: true},
				{Name: "Vanilla OS ARM64"},
			},
			expected: "amd64 artifact not found",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := selectArtifacts(test.artifacts)
			if err == nil || err.Error() != test.expected {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestExtractBuildRejectsUnsafePath(t *testing.T) {
	staging := t.TempDir()
	archivePath := filepath.Join(staging, "unsafe.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, err := archive.Create("../escape")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(entry, "content")
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := extractBuild(archivePath, staging, "amd64"); err == nil || !strings.Contains(err.Error(), "unsafe ZIP path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testRelease(runID int64, date string) release.Release {
	return release.Release{
		ID: runID,
		Artifacts: []release.Artifact{
			{Name: "Vanilla OS AMD64 " + date},
			{Name: "Vanilla OS ARM64 " + date},
		},
	}
}

func buildArchive(t *testing.T, arch, date string, badChecksum bool) []byte {
	t.Helper()
	name := isoName(arch, date)
	content := []byte("test ISO for " + arch + " " + date)
	digest := sha256.Sum256(content)
	digestText := hex.EncodeToString(digest[:])
	if badChecksum {
		digestText = strings.Repeat("0", sha256.Size*2)
	}

	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	iso, err := archive.Create("builds/" + strings.ToUpper(arch) + "/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iso.Write(content); err != nil {
		t.Fatal(err)
	}
	checksumName := strings.TrimSuffix(name, ".iso") + ".sha256.txt"
	checksum, err := archive.Create("builds/" + strings.ToUpper(arch) + "/" + checksumName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(checksum, "%s  builds/%s/%s\n", digestText, strings.ToUpper(arch), name); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func architectureFromPath(t *testing.T, path string) string {
	t.Helper()
	upper := strings.ToUpper(path)
	if strings.Contains(upper, "AMD64") {
		return "amd64"
	}
	if strings.Contains(upper, "ARM64") {
		return "arm64"
	}
	t.Fatalf("architecture missing from %q", path)
	return ""
}

func isoName(arch, date string) string {
	return "Vanilla-OS-3-stable-" + arch + "." + date + ".iso"
}

func assertPublished(t *testing.T, root, arch, date string) {
	t.Helper()
	name := isoName(arch, date)
	for _, path := range []string{name, strings.TrimSuffix(name, ".iso") + ".sha256.txt"} {
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Fatalf("unexpected mode for %s: %o", path, info.Mode().Perm())
		}
	}
	target, err := os.Readlink(filepath.Join(root, "latest-"+arch+".iso"))
	if err != nil {
		t.Fatal(err)
	}
	if target != name {
		t.Fatalf("unexpected link target %q", target)
	}
}

func assertManifest(t *testing.T, root, expectedDate string) {
	t.Helper()
	file, err := os.Open(filepath.Join(root, "downloads.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var manifest []download
	if err := json.NewDecoder(file).Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest) != 2 || manifest[0].Arch != "amd64" || manifest[1].Arch != "arm64" {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	for _, entry := range manifest {
		if entry.Date != expectedDate || !strings.HasPrefix(entry.Iso, "https://download.vanillaos.org/Vanilla-OS-3-stable-") || !strings.HasSuffix(entry.Sha256, ".sha256.txt") || strings.HasSuffix(entry.Sha256, ".iso.sha256.txt") {
			t.Fatalf("unexpected manifest entry: %#v", entry)
		}
	}
}
