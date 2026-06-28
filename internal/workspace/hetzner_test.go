package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestNewHetznerProvider(t *testing.T) {
	p := NewHetznerProvider("test-api-key")
	if p == nil {
		t.Fatal("expected non-nil HetznerProvider")
	}
}

// hetznerTestProvider creates a HetznerProvider backed by a test HTTP server.
// The returned closer must be called when done.
func hetznerTestProvider(handler http.Handler) (*HetznerProvider, func()) {
	srv := httptest.NewServer(handler)
	client := hcloud.NewClient(
		hcloud.WithToken("test-token"),
		hcloud.WithEndpoint(srv.URL),
	)
	p := &HetznerProvider{client: client}
	return p, srv.Close
}

// TestHetznerProvider_Create_RunningServer verifies that Create succeeds when
// the mock API returns a server that is already in "running" status.
func TestHetznerProvider_Create_RunningServer(t *testing.T) {
	mux := http.NewServeMux()

	// POST /servers — return a server in "starting" state.
	mux.HandleFunc("POST /servers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		resp := map[string]interface{}{
			"server": map[string]interface{}{
				"id":     float64(42),
				"name":   "test-server",
				"status": "starting",
				"public_net": map[string]interface{}{
					"ipv4": map[string]interface{}{
						"ip": "1.2.3.4",
					},
					"ipv6": map[string]interface{}{
						"ip": "2001:db8::/64",
					},
				},
			},
			"action": map[string]interface{}{
				"id":     float64(1),
				"status": "running",
			},
			"next_actions": []interface{}{},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// GET /servers/42 — return the server in "running" status.
	mux.HandleFunc("GET /servers/42", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"server": map[string]interface{}{
				"id":     float64(42),
				"name":   "test-server",
				"status": "running",
				"public_net": map[string]interface{}{
					"ipv4": map[string]interface{}{
						"ip": "1.2.3.4",
					},
					"ipv6": map[string]interface{}{
						"ip": "2001:db8::/64",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	p, close := hetznerTestProvider(mux)
	defer close()

	vm, err := p.Create(context.Background(), VMCreateOpts{
		Name:       "test-server",
		ImageName:  "ubuntu-24.04",
		ServerType: "cx22",
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if vm == nil {
		t.Fatal("expected non-nil VM")
	}
	if vm.ID != "42" {
		t.Errorf("VM.ID = %q, want %q", vm.ID, "42")
	}
	if vm.PublicIP != "1.2.3.4" {
		t.Errorf("VM.PublicIP = %q, want %q", vm.PublicIP, "1.2.3.4")
	}
}

// TestHetznerProvider_Create_APIError verifies that Create returns an error
// when the API returns a non-2xx response.
func TestHetznerProvider_Create_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/servers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		resp := map[string]interface{}{
			"error": map[string]interface{}{
				"code":    "invalid_input",
				"message": "invalid server type",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	p, close := hetznerTestProvider(mux)
	defer close()

	_, err := p.Create(context.Background(), VMCreateOpts{
		Name:       "test-server",
		ImageName:  "ubuntu-24.04",
		ServerType: "cx22",
	})
	if err == nil {
		t.Fatal("expected error for API error response, got nil")
	}
}

func TestHetznerProvider_CreateDeletesServerAfterPollingError(t *testing.T) {
	mux := http.NewServeMux()
	deleted := false

	mux.HandleFunc("/servers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		resp := map[string]interface{}{
			"server": map[string]interface{}{
				"id":     float64(123),
				"name":   "leaky-server",
				"status": "starting",
				"public_net": map[string]interface{}{
					"ipv4": map[string]interface{}{"ip": "1.2.3.4"},
					"ipv6": map[string]interface{}{"ip": "2001:db8::/64"},
				},
			},
			"action": map[string]interface{}{
				"id":     float64(10),
				"status": "running",
			},
			"next_actions": []interface{}{},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/servers/123", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"code":    "service_error",
					"message": "poll failed",
				},
			})
		case http.MethodDelete:
			deleted = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"action": map[string]interface{}{"id": float64(11), "status": "success"},
			})
		default:
			http.NotFound(w, r)
		}
	})

	p, closeServer := hetznerTestProvider(mux)
	defer closeServer()

	_, err := p.Create(context.Background(), VMCreateOpts{
		Name:       "leaky-server",
		ImageName:  "ubuntu-24.04",
		ServerType: "cx22",
	})
	if err == nil {
		t.Fatal("expected polling error")
	}
	if !deleted {
		t.Fatal("expected server delete after post-create polling error")
	}
}

// TestHetznerProvider_Delete_NotFound verifies that Delete treats a 404 as success.
func TestHetznerProvider_Delete_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/servers/", func(w http.ResponseWriter, r *http.Request) {
		// Return 404 for any server lookup — simulates already-deleted server.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		resp := map[string]interface{}{
			"error": map[string]interface{}{
				"code":    "not_found",
				"message": "server not found",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	p, close := hetznerTestProvider(mux)
	defer close()

	err := p.Delete(context.Background(), "99")
	if err != nil {
		t.Fatalf("Delete on not-found server should return nil, got: %v", err)
	}
}

// TestHetznerProvider_Delete_Success verifies that Delete succeeds for an existing server.
func TestHetznerProvider_Delete_Success(t *testing.T) {
	mux := http.NewServeMux()

	// GET /servers/10 — server exists.
	mux.HandleFunc("/servers/10", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := map[string]interface{}{
				"server": map[string]interface{}{
					"id":     float64(10),
					"name":   "to-delete",
					"status": "running",
					"public_net": map[string]interface{}{
						"ipv4": map[string]interface{}{"ip": "5.6.7.8"},
						"ipv6": map[string]interface{}{"ip": "::1/128"},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		case http.MethodDelete:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := map[string]interface{}{
				"action": map[string]interface{}{
					"id":     float64(2),
					"status": "success",
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	})

	p, close := hetznerTestProvider(mux)
	defer close()

	err := p.Delete(context.Background(), "10")
	if err != nil {
		t.Fatalf("Delete error: %v", err)
	}
}

// TestHetznerProvider_Delete_InvalidID verifies that Delete returns an error for a non-numeric ID.
func TestHetznerProvider_Delete_InvalidID(t *testing.T) {
	p := NewHetznerProvider("test-key")
	err := p.Delete(context.Background(), "not-a-number")
	if err == nil {
		t.Fatal("expected error for invalid ID, got nil")
	}
	if !strings.Contains(err.Error(), "invalid server ID") {
		t.Errorf("error should mention 'invalid server ID', got: %v", err)
	}
}

// TestHetznerProvider_Create_ContextCancelled verifies that Create returns an error
// when the context is already cancelled.
func TestHetznerProvider_Create_ContextCancelled(t *testing.T) {
	// Use a handler that blocks forever — context should cancel first.
	mux := http.NewServeMux()
	mux.HandleFunc("/servers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		// Return a valid response — cancellation is tested at the poll stage.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		resp := map[string]interface{}{
			"server": map[string]interface{}{
				"id":     float64(77),
				"name":   "test",
				"status": "initializing",
				"public_net": map[string]interface{}{
					"ipv4": map[string]interface{}{"ip": "9.9.9.9"},
					"ipv6": map[string]interface{}{"ip": "::1/128"},
				},
			},
			"action": map[string]interface{}{
				"id":     float64(3),
				"status": "running",
			},
			"next_actions": []interface{}{},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc(fmt.Sprintf("/servers/%d", 77), func(w http.ResponseWriter, r *http.Request) {
		// Always return "initializing" to trigger context cancel.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"server": map[string]interface{}{
				"id":     float64(77),
				"name":   "test",
				"status": "initializing",
				"public_net": map[string]interface{}{
					"ipv4": map[string]interface{}{"ip": "9.9.9.9"},
					"ipv6": map[string]interface{}{"ip": "::1/128"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	p, closeServer := hetznerTestProvider(mux)
	defer closeServer()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling Create

	_, err := p.Create(ctx, VMCreateOpts{
		Name:       "test",
		ImageName:  "ubuntu-24.04",
		ServerType: "cx22",
	})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}
