package skills

import (
	"context"
	"fmt"
)

// SkillResolver resolves a skill name + args into interpolated content.
type SkillResolver interface {
	ResolveSkill(ctx context.Context, name, args, workspace string) (string, error)
}

// Resolver implements SkillResolver using a Registry.
type Resolver struct {
	registry *Registry
}

// NewResolver creates a new Resolver backed by the given Registry.
func NewResolver(registry *Registry) *Resolver {
	return &Resolver{registry: registry}
}

// ResolveSkill looks up a skill by name, interpolates its body with the given
// arguments and workspace, and returns the result. Positional placeholders are
// 0-based ($0 is the first argument token); when the body references no
// argument placeholder, the raw args are appended as a trailing
// "ARGUMENTS: <args>" line. Shell command preprocessing (!`cmd`) is applied
// after variable interpolation.
func (r *Resolver) ResolveSkill(ctx context.Context, name, args, workspace string) (string, error) {
	skill, ok := r.registry.Get(name)
	if !ok {
		return "", fmt.Errorf("skill not found: %s", name)
	}

	content := expandBody(skill, args, workspace)
	content = preprocessCommands(ctx, content, workspace)
	return content, nil
}
