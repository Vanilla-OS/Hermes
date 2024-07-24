package downloader

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/vanilla-os/Hermes/pkg/release"
	"github.com/vanilla-os/Hermes/pkg/utils"

	"github.com/PuerkitoBio/goquery"
	"github.com/cavaliergopher/grab/v3"
)

var (
	downloadInProgress bool
	mu                 sync.Mutex
)

func CheckForNewRelease(releaseIndex, buildsPath string) {
	mu.Lock()
	if downloadInProgress {
		log.Println("download already in progress")
		mu.Unlock()
		return
	}
	downloadInProgress = true
	mu.Unlock()

	defer func() {
		mu.Lock()
		downloadInProgress = false
		mu.Unlock()
	}()

	releases, err := release.FetchReleases(releaseIndex)
	if err != nil {
		log.Printf("error fetching releases: %v", err)
		return
	}

	if len(releases) == 0 {
		log.Println("no releases found")
		return
	}

	latestRelease := releases[0]
	releaseID := latestRelease.Id
	releaseFileName := fmt.Sprintf("%s.zip", releaseID[1:])
	releaseFilePath := filepath.Join(buildsPath, releaseFileName)

	if !isDownloaded(releaseFilePath) {
		nightlyLink := fmt.Sprintf("https://nightly.link%s", extractRepoPath(latestRelease.Url))
		zipLink, err := getZipLink(nightlyLink)
		if err != nil {
			log.Fatalf("error retrieving zip link: %v", err)
		}
		downloadFile(releaseFilePath, zipLink)
	}
	utils.CreateSymlink(filepath.Base(releaseFilePath), filepath.Join(buildsPath, "latest.zip"))
	cleanupBuilds(buildsPath, 2)
}

func extractRepoPath(buildURL string) string {
	parsedURL, err := url.Parse(buildURL)
	if err != nil {
		log.Fatalf("error parsing URL %s: %v", buildURL, err)
	}
	// NOTE: Assuming the URL is in the form "https://github.com/owner/repo/actions/runs/ID"
	segments := strings.Split(parsedURL.Path, "/")
	if len(segments) < 6 {
		log.Fatalf("unexpected URL format: %s", buildURL)
	}
	return strings.Join(segments[:6], "/")
}

func getZipLink(nightlyLink string) (string, error) {
	resp, err := http.Get(nightlyLink)
	if err != nil {
		return "", fmt.Errorf("http request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http request error: status code %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error loading HTTP response body: %v", err)
	}

	zipLink := ""
	doc.Find("a").Each(func(index int, item *goquery.Selection) {
		href, exists := item.Attr("href")
		if exists && strings.HasSuffix(href, ".zip") {
			zipLink = href
			return
		}
	})

	if zipLink == "" {
		return "", fmt.Errorf("zip link not found")
	}

	return zipLink, nil
}

func isDownloaded(filePath string) bool {
	_, err := os.Stat(filePath)
	return !os.IsNotExist(err)
}

func downloadFile(filePath, url string) {
	log.Printf("downloading release from %s to %s", url, filePath)
	client := grab.NewClient()
	req, _ := grab.NewRequest(filePath, url)

	resp := client.Do(req)
	if err := resp.Err(); err != nil {
		log.Printf("download error: %v", err)
		return
	}

	log.Printf("download completed: %s", resp.Filename)
}

func cleanupBuilds(buildsPath string, keep int) {
	files, err := os.ReadDir(buildsPath)
	if err != nil {
		log.Fatalf("error reading directory %s: %v", buildsPath, err)
	}

	var buildFiles []os.DirEntry
	for _, file := range files {
		if file.Name() != "latest.zip" {
			buildFiles = append(buildFiles, file)
		}
	}

	if len(buildFiles) <= keep {
		return
	}

	sort.Slice(buildFiles, func(i, j int) bool {
		infoI, errI := buildFiles[i].Info()
		infoJ, errJ := buildFiles[j].Info()
		if errI != nil || errJ != nil {
			log.Fatalf("error retrieving file info: %v, %v", errI, errJ)
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})

	for i := keep; i < len(buildFiles); i++ {
		filePath := filepath.Join(buildsPath, buildFiles[i].Name())
		if err := os.Remove(filePath); err != nil {
			log.Printf("error deleting file %s: %v", filePath, err)
		} else {
			log.Printf("file deleted: %s", filePath)
		}
	}
}
