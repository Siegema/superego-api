package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if err := run(context.Background()); err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("expected explicit configuration failure, got %v", err)
	}
}
