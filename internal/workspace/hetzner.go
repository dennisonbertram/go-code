package workspace

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

const (
	hetznerDefaultImage = "ubuntu-24.04"
	// hetznerDefaultServerType picks the cheapest current SKU. The
	// previous default (cx22) was deprecated by Hetzner; attempts to create
	// it now fail with "server type 104 is deprecated". cax11 is the
	// cheapest current SKU at the time of writing (~€0.0088/hr, ARM, 2c/4G).
	// cpx11 is the cheapest x86 alternative but isn't available in the
	// EU locations many free-tier projects are pinned to.
	hetznerDefaultServerType = "cax11"
	// hetznerDefaultLocation pins the datacenter when none is requested.
	// Without an explicit location, Hetzner picks a default that may not
	// support the chosen server type ("unsupported location for server type").
	// nbg1 (Nuremberg) supports cax* SKUs in all observed accounts.
	hetznerDefaultLocation  = "nbg1"
	hetznerPollInterval     = 3 * time.Second
	hetznerProvisionTimeout = 5 * time.Minute
)

// HetznerProvider implements VMProvider using the Hetzner Cloud API.
type HetznerProvider struct {
	client *hcloud.Client
}

// NewHetznerProvider creates a HetznerProvider authenticated with the given API key.
func NewHetznerProvider(apiKey string) *HetznerProvider {
	client := hcloud.NewClient(hcloud.WithToken(apiKey))
	return &HetznerProvider{client: client}
}

// Create provisions a new Hetzner Cloud server and waits until it is running.
// It polls every 3 seconds with a 5-minute timeout. The returned VM contains
// the server's string ID and its public IPv4 address.
func (p *HetznerProvider) Create(ctx context.Context, opts VMCreateOpts) (*VM, error) {
	imageName := opts.ImageName
	if imageName == "" {
		imageName = hetznerDefaultImage
	}
	serverType := opts.ServerType
	if serverType == "" {
		serverType = hetznerDefaultServerType
	}

	createOpts := hcloud.ServerCreateOpts{
		Name:       hetznerSanitizeName(opts.Name),
		ServerType: &hcloud.ServerType{Name: serverType},
		Image:      &hcloud.Image{Name: imageName},
		Location:   &hcloud.Location{Name: hetznerDefaultLocation},
		UserData:   opts.UserData,
	}

	result, _, err := p.client.Server.Create(ctx, createOpts)
	if err != nil {
		return nil, fmt.Errorf("hetzner: server create: %w", err)
	}

	server := result.Server
	if server == nil {
		return nil, fmt.Errorf("hetzner: server create returned nil server")
	}
	cleanupCreatedServer := func() {
		forceCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = p.client.Server.Delete(forceCtx, server)
	}

	// Poll until the server reaches running status.
	deadline := time.Now().Add(hetznerProvisionTimeout)
	for {
		if ctx.Err() != nil {
			cleanupCreatedServer()
			return nil, fmt.Errorf("hetzner: context cancelled while waiting for server: %w", ctx.Err())
		}
		if time.Now().After(deadline) {
			cleanupCreatedServer()
			return nil, fmt.Errorf("hetzner: timed out waiting for server %d to reach running status", server.ID)
		}

		updated, _, err := p.client.Server.GetByID(ctx, server.ID)
		if err != nil {
			cleanupCreatedServer()
			return nil, fmt.Errorf("hetzner: polling server status: %w", err)
		}
		if updated == nil {
			cleanupCreatedServer()
			return nil, fmt.Errorf("hetzner: server %d disappeared during provisioning", server.ID)
		}

		if updated.Status == hcloud.ServerStatusRunning {
			return &VM{
				ID:       strconv.FormatInt(updated.ID, 10),
				PublicIP: updated.PublicNet.IPv4.IP.String(),
				Status:   string(updated.Status),
			}, nil
		}

		select {
		case <-ctx.Done():
			cleanupCreatedServer()
			return nil, fmt.Errorf("hetzner: context cancelled while waiting for server: %w", ctx.Err())
		case <-time.After(hetznerPollInterval):
		}
	}
}

// hetznerNameInvalidRe matches characters that aren't valid in a Hetzner
// server name. Hetzner enforces RFC 1035-style DNS hostnames: lowercase
// alphanumerics and hyphens only, no underscores, no uppercase, max 63 chars.
// The shared sanitizeBranch helper allows underscores (legal in git refs and
// in Docker container names) so we must do another pass here for Hetzner.
var hetznerNameInvalidRe = regexp.MustCompile(`[^a-z0-9-]`)

// hetznerSanitizeName normalizes a server name to Hetzner's allowed character
// set: lowercase, replace any disallowed character with '-', collapse runs of
// hyphens, strip leading/trailing hyphens, and truncate to 63 chars. If the
// input would yield an empty result it falls back to "workspace".
func hetznerSanitizeName(name string) string {
	s := strings.ToLower(name)
	s = hetznerNameInvalidRe.ReplaceAllString(s, "-")
	// Collapse consecutive hyphens.
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	if s == "" {
		return "workspace"
	}
	return s
}

// Delete terminates the Hetzner Cloud server with the given string ID.
// The ID must be the decimal string representation of the server's integer ID.
func (p *HetznerProvider) Delete(ctx context.Context, id string) error {
	serverID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return fmt.Errorf("hetzner: invalid server ID %q: %w", id, err)
	}

	server, _, err := p.client.Server.GetByID(ctx, serverID)
	if err != nil {
		return fmt.Errorf("hetzner: get server %d: %w", serverID, err)
	}
	if server == nil {
		// Already gone — treat as success.
		return nil
	}

	_, err = p.client.Server.Delete(ctx, server)
	if err != nil {
		return fmt.Errorf("hetzner: delete server %d: %w", serverID, err)
	}
	return nil
}
