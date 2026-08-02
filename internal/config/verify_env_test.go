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

	want := ">cI#+H!z5sUo=`j,&'^z"
	if cfg.AdminPass != want {
		t.Fatalf("AdminPass = %q, want %q", cfg.AdminPass, want)
	}
	if cfg.LoginRateLimit != 30 {
		t.Fatalf("LoginRateLimit = %d, want 30", cfg.LoginRateLimit)
	}
	if cfg.ApiDomain != "api-fleet.malaxis.ru" {
		t.Fatalf("ApiDomain = %q", cfg.ApiDomain)
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
