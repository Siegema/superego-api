package database

import (
	"context"
	"testing"
)

func TestOpenRequiresURL(t *testing.T) {
	pool, err := Open(context.Background(), "")
	if err == nil || pool != nil {
		t.Fatal("an explicit database URL is required")
	}
}

func TestOpenRejectsInvalidURL(t *testing.T) {
	pool, err := Open(context.Background(), "://invalid")
	if err == nil || pool != nil {
		t.Fatal("invalid database configuration should fail")
	}
}
