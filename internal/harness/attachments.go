package harness

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// ContentBlockTypeImage is the only non-text block type currently supported
// on the run boundary.
const ContentBlockTypeImage = "image"

// supportedImageMediaTypes are the image MIME types accepted on the run
// boundary: the TUI clipboard path produces PNG, and the ReadMediaFile tool
// (epic #818 slice 5) also accepts JPEG.
var supportedImageMediaTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
}

// validateImageAttachments enforces the run-boundary contract for image
// attachments: supported type and media type, non-empty valid base64 payload.
// Every error names the offending attachment index.
func validateImageAttachments(atts []ContentBlock) error {
	for i, a := range atts {
		if a.Type != ContentBlockTypeImage {
			return fmt.Errorf("attachments[%d]: unsupported type %q: only %q is supported", i, a.Type, ContentBlockTypeImage)
		}
		if !supportedImageMediaTypes[a.MediaType] {
			return fmt.Errorf("attachments[%d]: unsupported media_type %q: want image/png or image/jpeg", i, a.MediaType)
		}
		if strings.TrimSpace(a.Data) == "" {
			return fmt.Errorf("attachments[%d]: data is empty: image payload required", i)
		}
		raw, err := base64.StdEncoding.DecodeString(a.Data)
		if err != nil {
			return fmt.Errorf("attachments[%d]: data is not valid base64: %w", i, err)
		}
		if len(raw) == 0 {
			return fmt.Errorf("attachments[%d]: data decodes to an empty payload", i)
		}
	}
	return nil
}

// gateImageAttachmentModality returns an actionable error when the effective
// model is known — via the provider registry's catalog — to lack the image
// modality. Unknown data is allowed: nil registry, model absent from the
// catalog (e.g. discovered at runtime), or an entry without modalities all
// skip the gate; the provider call remains the deeper source of truth
// (provider-side refusal lands in epic #818 slice 4).
func (r *Runner) gateImageAttachmentModality(model, preferredProvider string) error {
	if r.providerRegistry == nil {
		return nil
	}
	providerName := preferredProvider
	if providerName == "" {
		var found bool
		providerName, found = r.providerRegistry.ResolveProvider(model)
		if !found {
			return nil
		}
	}
	cat := r.providerRegistry.Catalog()
	if cat == nil {
		return nil
	}
	info, ok := cat.ModelInfo(providerName, model)
	if !ok || len(info.Model.Modalities) == 0 {
		return nil
	}
	for _, mod := range info.Model.Modalities {
		if mod == "image" {
			return nil
		}
	}
	return fmt.Errorf("model %s (provider %s) does not support image input: remove the image attachments or switch to a model with the image modality", model, providerName)
}
