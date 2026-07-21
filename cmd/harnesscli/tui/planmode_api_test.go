package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStartRunCmdSendsPlanMode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body runCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.PlanMode {
			t.Fatal("plan_mode was not sent")
		}
		_, _ = w.Write([]byte(`{"run_id":"r"}`))
	}))
	defer ts.Close()
	if _, ok := startRunCmd(ts.URL, "p", "", "", "", "", "", "", "", nil, true)().(RunStartedMsg); !ok {
		t.Fatal("run not started")
	}
}
