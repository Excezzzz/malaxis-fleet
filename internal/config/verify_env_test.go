package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComplexPasswordEnv(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatal(err)
	}

	cfg := LoadConfig()

	if cfg.AdminPass == "" {
		t.Fatalf("AdminPass is empty; ADMIN_PASS must be set in the environment")
	}
	if cfg.LoginRateLimit <= 0 {
		t.Fatalf("LoginRateLimit = %d, want > 0", cfg.LoginRateLimit)
	}
	if cfg.ApiDomain == "" {
		t.Fatalf("ApiDomain must be set via API_DOMAIN")
	}
}

func TestUnwrapQuotesOnlyWhenWrapped(t *testing.T) {
	os.Setenv("TEST_VAL", "\"quoted\"")
	if got := getStringDefault("TEST_VAL", ""); got != "quoted" {
		t.Fatalf("wrapped double: got %q", got)
	}
	os.Setenv("TEST_VAL", "'single'")
	if got := getStringDefault("TEST_VAL", ""); got != "single" {
		t.Fatalf("wrapped single: got %q", got)
	}
	os.Setenv("TEST_VAL", "pa\"ss\"word")
	if got := getStringDefault("TEST_VAL", ""); got != "pa\"ss\"word" {
		t.Fatalf("inner quotes: got %q", got)
	}
	os.Setenv("TEST_VAL", "tail'")
	if got := getStringDefault("TEST_VAL", ""); got != "tail'" {
		t.Fatalf("trailing quote only: got %q", got)
	}
}
