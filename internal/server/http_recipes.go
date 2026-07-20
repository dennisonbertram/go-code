package server

import (
	"fmt"
	"net/http"
	"strings"

	"go-agent-harness/internal/harness/tools/recipe"
)

// recipeEntry is the JSON response shape for a recipe.
type recipeEntry struct {
	Name        string                         `json:"name"`
	Description string                         `json:"description"`
	Tags        []string                       `json:"tags"`
	Parameters  map[string]recipe.ParameterDef `json:"parameters,omitempty"`
}

func recipeToEntry(r recipe.Recipe) recipeEntry {
	tags := r.Tags
	if tags == nil {
		tags = []string{}
	}
	return recipeEntry{
		Name:        r.Name,
		Description: r.Description,
		Tags:        tags,
		Parameters:  r.Parameters,
	}
}

// handleRecipes routes requests under /v1/recipes.
func (s *Server) handleRecipes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/recipes")
	// strip leading slash
	path = strings.TrimPrefix(path, "/")

	if path == "" {
		// GET /v1/recipes
		s.handleListRecipes(w, r)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	name := parts[0]
	var sub string
	if len(parts) == 2 {
		sub = parts[1]
	}

	if sub == "schema" {
		s.handleRecipeSchema(w, r, name)
		return
	}
	if sub == "" {
		s.handleGetRecipe(w, r, name)
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleListRecipes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	entries := make([]recipeEntry, 0, len(s.recipes))
	for _, rc := range s.recipes {
		entries = append(entries, recipeToEntry(rc))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count":   len(entries),
		"recipes": entries,
	})
}

func (s *Server) handleGetRecipe(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	rc, ok := s.findRecipe(name)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("recipe %q not found", name))
		return
	}
	writeJSON(w, http.StatusOK, recipeToEntry(rc))
}

func (s *Server) handleRecipeSchema(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	rc, ok := s.findRecipe(name)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("recipe %q not found", name))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"parameters": rc.Parameters})
}

// findRecipe looks up a recipe by name in the server's recipe list.
func (s *Server) findRecipe(name string) (recipe.Recipe, bool) {
	for _, rc := range s.recipes {
		if rc.Name == name {
			return rc, true
		}
	}
	return recipe.Recipe{}, false
}
