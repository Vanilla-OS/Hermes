package release

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const userAgent = "Vanilla-OS-Hermes"

type workflowRunsResponse struct {
	WorkflowRuns []struct {
		ID           int64  `json:"id"`
		ArtifactsURL string `json:"artifacts_url"`
	} `json:"workflow_runs"`
}

type artifactsResponse struct {
	Artifacts []Artifact `json:"artifacts"`
}

func FetchLatest(ctx context.Context, client *http.Client, apiURL, repository, workflow, branch, token string) (Release, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/actions/workflows/%s/runs", strings.TrimRight(apiURL, "/"), repository, url.PathEscape(workflow))
	query := url.Values{}
	query.Set("branch", branch)
	query.Set("status", "success")
	query.Set("per_page", "1")

	var runs workflowRunsResponse
	if err := getJSON(ctx, client, endpoint+"?"+query.Encode(), token, &runs); err != nil {
		return Release{}, fmt.Errorf("fetch workflow runs: %w", err)
	}
	if len(runs.WorkflowRuns) == 0 {
		return Release{}, fmt.Errorf("no successful workflow runs found")
	}

	run := runs.WorkflowRuns[0]
	var artifacts artifactsResponse
	if err := getJSON(ctx, client, run.ArtifactsURL, token, &artifacts); err != nil {
		return Release{}, fmt.Errorf("fetch workflow artifacts: %w", err)
	}

	return Release{
		ID:        run.ID,
		Artifacts: artifacts.Artifacts,
	}, nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint, token string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
