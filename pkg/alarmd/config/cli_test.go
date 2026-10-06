package config

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCLIAdminKeyLoadsWithoutAppearingInJSON(t *testing.T) {
	key := strings.Repeat("k", 64)
	var cfg CLIConfig
	if err := yaml.Unmarshal([]byte("enabled: true\nadmin_key: "+key+"\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.AdminKey != key {
		t.Fatal("administrator key not loaded")
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) || strings.Contains(string(raw), "admin_key") || strings.Contains(string(raw), "AdminKey") {
		t.Fatal("administrator key in JSON evidence")
	}
}

// The administrator key may come from the environment, so that a chart can
// reference a Secret the operator created instead of carrying the key in
// values. Stated in both places it is refused rather than one silently
// winning, and an empty environment leaves the file's key alone.
func TestCLIAdminKeyComesFromTheEnvironment(t *testing.T) {
	key := strings.Repeat("e", 64)
	t.Setenv(CLIAdminKeyEnvironment, key)
	fromEnv := CLIConfig{Enabled: true}
	if err := fromEnv.resolveAdminKeyFromEnvironment(); err != nil || fromEnv.AdminKey != key {
		t.Fatalf("key not taken from the environment: %v", err)
	}
	both := CLIConfig{AdminKey: strings.Repeat("f", 64)}
	if err := both.resolveAdminKeyFromEnvironment(); err == nil {
		t.Fatal("a key stated in the file and the environment was accepted")
	}
	t.Setenv(CLIAdminKeyEnvironment, "")
	fileOnly := CLIConfig{AdminKey: strings.Repeat("f", 64)}
	if err := fileOnly.resolveAdminKeyFromEnvironment(); err != nil || fileOnly.AdminKey != strings.Repeat("f", 64) {
		t.Fatalf("an empty environment overrode the file: %v", err)
	}
}

// Loading applies it: a file that enables the CLI without a key loads with
// the key the environment carries.
func TestLoadingTakesTheCLIAdminKeyFromTheEnvironment(t *testing.T) {
	key := strings.Repeat("e", 64)
	t.Setenv(CLIAdminKeyEnvironment, key)
	cfg, err := Load(writeConfig(t, validGoAccessRuntimeConfigYAML("cli-worker")+"cli:\n  enabled: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CLI.AdminKey != key {
		t.Fatal("loading did not take the administrator key from the environment")
	}
}
