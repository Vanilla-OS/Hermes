package release

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchLatest(t *testing.T) {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected authorization header %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/repos/Vanilla-OS/live-iso/actions/workflows/build-iso.yml/runs":
			if r.URL.Query().Get("branch") != "orchid" || r.URL.Query().Get("status") != "success" || r.URL.Query().Get("per_page") != "1" {
				t.Fatalf("unexpected query %q", r.URL.RawQuery)
			}
			fmt.Fprintf(w, `{"workflow_runs":[{"id":42,"run_number":7,"created_at":"2026-08-26T12:00:00Z","html_url":"https://github.com/Vanilla-OS/live-iso/actions/runs/42","artifacts_url":%q}]}`, server.URL+"/artifacts")
		case "/artifacts":
			fmt.Fprint(w, `{"artifacts":[{"id":1,"name":"Vanilla OS AMD64 2026-08-26","expired":false},{"id":2,"name":"Vanilla OS ARM64 2026-08-26","expired":false}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	latest, err := FetchLatest(context.Background(), server.Client(), server.URL, "Vanilla-OS/live-iso", "build-iso.yml", "orchid", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != 42 || len(latest.Artifacts) != 2 {
		t.Fatalf("unexpected release: %#v", latest)
	}
}

func TestFetchLatestWithoutSuccessfulRuns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"workflow_runs":[]}`)
	}))
	defer server.Close()

	_, err := FetchLatest(context.Background(), server.Client(), server.URL, "Vanilla-OS/live-iso", "build-iso.yml", "orchid", "")
	if err == nil {
		t.Fatal("expected an error")
	}
}
