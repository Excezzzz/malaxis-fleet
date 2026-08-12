package api

import (
	"embed"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

//go:embed testdata
var dashboardTestFS embed.FS

func TestServeDashboardSpaFallback(t *testing.T) {
	sub, err := fs.Sub(dashboardTestFS, "testdata")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	ts := httptest.NewServer(serveDashboard(sub))
	defer ts.Close()

	cases := []struct {
		path string
		want string
	}{
		{"/", "INDEX"},
		{"/index.html", "INDEX"},
		{"/js/app.js", "//app"},
		{"/login", "INDEX"},
		{"/devices/overview", "INDEX"},
	}
	for _, tc := range cases {
		resp, err := http.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		body := make([]byte, 512)
		n, _ := resp.Body.Read(body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", tc.path, resp.StatusCode)
		}
		if !strings.Contains(string(body[:n]), tc.want) {
			t.Errorf("GET %s: body = %q, want %q", tc.path, string(body[:n]), tc.want)
		}
	}
}