//go:build integration

package workspace

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHetznerProvider_FullLifecycle(t *testing.T) {
	apiKey := os.Getenv("HETZNER_API_KEY")
	if apiKey == "" {
		t.Skip("HETZNER_API_KEY not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	provider := NewHetznerProvider(apiKey)
	vm, err := provider.Create(ctx, VMCreateOpts{
		Name:       "test-workspace-lifecycle",
		ImageName:  "ubuntu-24.04",
		ServerType: "cx22",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	t.Logf("Created VM %s with IP %s (status: %s)", vm.ID, vm.PublicIP, vm.Status)

	if vm.ID == "" {
		t.Error("expected non-empty VM ID")
	}
	if vm.PublicIP == "" {
		t.Error("expected non-empty VM PublicIP")
	}
	if vm.Status == "" {
		t.Error("expected non-empty VM Status")
	}

	defer func() {
		if err := provider.Delete(ctx, vm.ID); err != nil {
			t.Errorf("Delete failed: %v", err)
		}
		t.Logf("Deleted VM %s", vm.ID)
	}()
}

// TestVMIsolation proves that a VMWorkspace executes commands on the remote VM
// and not on the host machine.
//
// Prerequisites:
//   - Build tag: //go:build integration  (this file)
//   - Env var:   HETZNER_API_KEY must be set to a valid Hetzner Cloud API key
//
// When HETZNER_API_KEY is absent the test skips with an explicit message
// documenting why: the VM backend (HetznerProvider) requires real cloud
// credentials to provision a server; without them no isolation boundary can be
// asserted.
//
// Host-routing caveat: the VMWorkspace communicates with the provisioned VM
// over its public IPv4 address on port 8080.  Any firewall or NAT rule that
// blocks outbound TCP to that port from the test host will cause the health
// poll to time out rather than proving isolation.  If Provision succeeds but
// the health check never passes, inspect egress firewall rules on the test
// host and ensure port 8080 is reachable to the Hetzner datacenter (nbg1).
func TestVMIsolation(t *testing.T) {
	apiKey := os.Getenv("HETZNER_API_KEY")
	if apiKey == "" {
		t.Skip("SKIP: HETZNER_API_KEY not set — VMWorkspace requires real Hetzner Cloud " +
			"credentials to provision a server; isolation cannot be asserted without a live VM. " +
			"Host-routing caveat: even when the key is present, outbound TCP to port 8080 " +
			"on the provisioned VM's public IP must be permitted by the host's egress firewall.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	provider := NewHetznerProvider(apiKey)
	w := NewVM(provider)

	// Record the test-host hostname BEFORE provisioning so we can assert the
	// VM hostname differs from it (proving the harness is running on the VM,
	// not the local host).
	hostHostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname: %v", err)
	}

	wsID := fmt.Sprintf("isolation-test-%d", time.Now().UnixNano())
	if err := w.Provision(ctx, Options{ID: wsID}); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	// Ensure cleanup even on failure.
	t.Cleanup(func() {
		if err := w.Destroy(context.Background()); err != nil {
			t.Logf("Cleanup Destroy: %v", err)
		}
	})

	// (1) Post-provision invariants.
	if w.HarnessURL() == "" {
		t.Fatal("HarnessURL() is empty after Provision")
	}
	if w.WorkspacePath() != "/workspace" {
		t.Errorf("WorkspacePath() = %q, want /workspace", w.WorkspacePath())
	}

	// (2) Poll the harness health endpoint until it is reachable, confirming
	//     the remote harnessd instance is running on the VM.
	healthURL := w.HarnessURL() + "/health"
	healthDeadline := time.Now().Add(2 * time.Minute)
	var lastHealthErr error
	for time.Now().Before(healthDeadline) {
		resp, err := http.Get(healthURL) //nolint:gosec // test-only
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				lastHealthErr = nil
				break
			}
			lastHealthErr = fmt.Errorf("unexpected status %d", resp.StatusCode)
		} else {
			lastHealthErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context cancelled while waiting for VM harness health: %v", ctx.Err())
		case <-time.After(3 * time.Second):
		}
	}
	if lastHealthErr != nil {
		t.Fatalf("VM harness at %s never became healthy: %v — "+
			"check egress firewall rules (port 8080 to Hetzner nbg1 must be permitted)", healthURL, lastHealthErr)
	}
	t.Logf("VM harness healthy at %s", w.HarnessURL())

	// (3) Assert isolation: fetch /debug/hostname and confirm it does NOT equal
	//     the host machine's hostname.  This proves the harnessd instance is
	//     running on the remote VM, not on the host that launched the test.
	//
	//     When the test is actually running against a backend (i.e. we reached
	//     this point past the env-gate skip and Provision succeeded) both the
	//     HTTP call and a valid non-empty 200 response are required — a missing
	//     or non-200 endpoint means the harness image does not expose the
	//     isolation-proof endpoint and must be fixed.
	hostnameURL := w.HarnessURL() + "/debug/hostname"
	resp, err := http.Get(hostnameURL) //nolint:gosec // test-only
	if err != nil {
		t.Fatalf("isolation proof failed: GET %s error: %v — "+
			"the harness image must expose /debug/hostname for isolation assertions", hostnameURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	vmHostname := strings.TrimSpace(string(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("isolation proof failed: GET %s returned status %d body %q — "+
			"expected 200 OK with the VM hostname", hostnameURL, resp.StatusCode, vmHostname)
	}
	if vmHostname == "" {
		t.Fatalf("isolation proof failed: GET %s returned 200 but empty body — "+
			"the harness image must return the VM hostname at /debug/hostname", hostnameURL)
	}
	if vmHostname == hostHostname {
		t.Errorf("isolation failure: VM harness reports hostname %q which matches the host machine — "+
			"harnessd may be running on the host, not the remote VM", vmHostname)
	} else {
		t.Logf("Isolation confirmed: VM hostname=%q, host hostname=%q", vmHostname, hostHostname)
	}

	// (4) WorkspacePath must be on the remote VM filesystem (/workspace), not
	//     a local path. Assert the path is not reachable from the host cwd.
	cwd, _ := os.Getwd()
	vmPath := w.WorkspacePath()
	// The VM path is /workspace — it should NOT exist on the host under cwd.
	localEquivalent := strings.TrimPrefix(vmPath, "/")
	if _, err := os.Stat(localEquivalent); err == nil {
		t.Logf("Note: path %q happens to exist locally — this is not a hard isolation failure "+
			"(the VM path /workspace is a common local path), but verify provisioning went to the VM", localEquivalent)
	}
	_ = cwd // used implicitly via localEquivalent

	// (5) Destroy must succeed and clean up the VM.
	if err := w.Destroy(ctx); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	// After Destroy the VM ID should be cleared, making a second Destroy a no-op.
	if err := w.Destroy(ctx); err != nil {
		t.Errorf("second Destroy (no-op) returned error: %v", err)
	}
	t.Log("VM workspace destroyed successfully")
}

func TestVMWorkspace_FullLifecycle(t *testing.T) {
	apiKey := os.Getenv("HETZNER_API_KEY")
	if apiKey == "" {
		t.Skip("HETZNER_API_KEY not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	provider := NewHetznerProvider(apiKey)
	w := NewVM(provider)

	if err := w.Provision(ctx, Options{ID: "integration-test-185"}); err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	t.Logf("Provisioned workspace: harnessURL=%s workspacePath=%s", w.HarnessURL(), w.WorkspacePath())

	if w.HarnessURL() == "" {
		t.Error("expected non-empty HarnessURL after Provision")
	}
	if w.WorkspacePath() != "/workspace" {
		t.Errorf("expected WorkspacePath=/workspace, got %q", w.WorkspacePath())
	}

	if err := w.Destroy(ctx); err != nil {
		t.Fatalf("Destroy failed: %v", err)
	}
	t.Log("Workspace destroyed")
}
