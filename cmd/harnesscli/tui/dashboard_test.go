package tui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDashboardRunListCmdLoadsRunsAndModelStoresThem(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("auth = %q", got)
		}
		_, _ = w.Write([]byte(`{"runs":[{"id":"run-1","status":"running","model":"gpt","prompt":"work"}]}`))
	}))
	defer ts.Close()

	msg := loadDashboardRunsCmd(ts.URL, "test-key")()
	m := New(TUIConfig{BaseURL: ts.URL})
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if len(m.dashboard.runs) != 1 || m.dashboard.runs[0].displayID() != "run-1" {
		t.Fatalf("dashboard runs = %#v", m.dashboard.runs)
	}
}
