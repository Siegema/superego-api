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

func TestHTTPAddress(t *testing.T) {
	for _, tt := range []struct {
		name string
		env  string
		want string
	}{
		{name: "local default", want: "127.0.0.1:8080"},
		{name: "container override", env: "0.0.0.0:8080", want: "0.0.0.0:8080"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HTTP_ADDR", tt.env)
			if got := httpAddress(); got != tt.want {
				t.Fatalf("httpAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}
