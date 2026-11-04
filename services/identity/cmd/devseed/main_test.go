package main

import (
	"strings"
	"testing"
)

func TestRunRequiresDevelopmentEnvironment(t *testing.T) {
	t.Setenv("APP_ENV", "production")

	err := run()
	if err == nil || !strings.Contains(err.Error(), "APP_ENV must be development") {
		t.Fatalf("run() error = %v, want development-only guard", err)
	}
}
