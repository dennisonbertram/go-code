package harness

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLiteConversationStorePlanContentSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.db")
	s, err := NewSQLiteConversationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlanContent(context.Background(), "conv", "run", "# plan"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteConversationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadPlanContent(context.Background(), "conv")
	if err != nil {
		t.Fatal(err)
	}
	if got != "# plan" {
		t.Fatalf("plan=%q", got)
	}
}
