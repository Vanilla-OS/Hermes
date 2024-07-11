package release

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

func FetchReleases(releaseIndex string) ([]Release, error) {
	resp, err := http.Get(releaseIndex)
	if err != nil {
		return nil, fmt.Errorf("http request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http request error: status code %d", resp.StatusCode)
	}

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("json decode error: %v", err)
	}

	sort.Slice(releases, func(i, j int) bool {
		return releases[i].Date > releases[j].Date
	})

	return releases, nil
}
