package provider

import "errors"

// ErrImageModalityUnsupported is returned by provider clients when a request
// carries an image content block for a model the catalog marks as lacking
// the image modality. It is defense in depth beneath the runner's modality
// gate (epic #818 slice 3): callers can detect it with errors.Is regardless
// of which provider client produced it.
var ErrImageModalityUnsupported = errors.New("model does not support image input")
