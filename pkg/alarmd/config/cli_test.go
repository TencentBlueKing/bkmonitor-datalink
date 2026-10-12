package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCLIAdminKeyLoadsWithoutAppearingInJSON(t *testing.T) {
	key := strings.Repeat("k", 64)
	var cfg CLIConfig
	if err := yaml.Unmarshal([]byte("admin_key: "+key+"\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled() || cfg.AdminKey != key {
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
	fromEnv := CLIConfig{}
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

// Loading applies it: a file that states no key loads with the key the
// environment carries, and the CLI is on.
func TestLoadingTakesTheCLIAdminKeyFromTheEnvironment(t *testing.T) {
	key := strings.Repeat("e", 64)
	t.Setenv(CLIAdminKeyEnvironment, key)
	cfg, err := Load(writeConfig(t, validGoAccessRuntimeConfigYAML("cli-worker")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CLI.AdminKey != key || !cfg.CLI.Enabled() {
		t.Fatal("loading did not take the administrator key from the environment and turn the CLI on")
	}
}

// The login page shows the command that reads the administrator key, so the
// process learns where the key is kept: the namespace from the Pod's own
// namespace file, falling back to POD_NAMESPACE only when that file is not
// mounted, and the Secret's name and key from the environment the chart
// fills. Unset, each stays empty rather than guessed.
func TestTheAdminKeySecretIsReadFromThePodAndTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	mounted := filepath.Join(dir, "namespace")
	if err := os.WriteFile(mounted, []byte("ops-alarmd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := serviceAccountNamespacePath
	t.Cleanup(func() { serviceAccountNamespacePath = previous })

	serviceAccountNamespacePath = mounted
	t.Setenv(PodNamespaceEnvironment, "from-env")
	t.Setenv(CLIAdminKeySecretNameEnvironment, " alarmd-cli-admin ")
	t.Setenv(CLIAdminKeySecretKeyEnvironment, "admin.key")
	var cli CLIConfig
	cli.resolveAdminKeySecret()
	if want := (CLIAdminKeySecret{Namespace: "ops-alarmd", Name: "alarmd-cli-admin", Key: "admin.key"}); cli.AdminKeySecret != want {
		t.Fatalf("with the file and both names: %+v, want %+v", cli.AdminKeySecret, want)
	}

	serviceAccountNamespacePath = filepath.Join(dir, "not-mounted")
	cli.resolveAdminKeySecret()
	if cli.AdminKeySecret.Namespace != "from-env" {
		t.Fatalf("without the file the namespace is POD_NAMESPACE's: %+v", cli.AdminKeySecret)
	}

	for _, name := range []string{PodNamespaceEnvironment, CLIAdminKeySecretNameEnvironment, CLIAdminKeySecretKeyEnvironment} {
		t.Setenv(name, "")
	}
	cli.resolveAdminKeySecret()
	if cli.AdminKeySecret != (CLIAdminKeySecret{}) {
		t.Fatalf("with nothing to read every name stays empty: %+v", cli.AdminKeySecret)
	}
}

// Loading applies it, and the names never reach JSON evidence or come from
// the file: the file has no field for them.
func TestLoadingReadsTheAdminKeySecretAndTheFileCannotStateIt(t *testing.T) {
	key := strings.Repeat("e", 64)
	t.Setenv(CLIAdminKeyEnvironment, key)
	t.Setenv(CLIAdminKeySecretNameEnvironment, "alarmd-cli-admin")
	t.Setenv(CLIAdminKeySecretKeyEnvironment, "admin-key")
	cfg, err := Load(writeConfig(t, validGoAccessRuntimeConfigYAML("cli-worker")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CLI.AdminKeySecret.Name != "alarmd-cli-admin" || cfg.CLI.AdminKeySecret.Key != "admin-key" {
		t.Fatalf("loading did not read the Secret's names: %+v", cfg.CLI.AdminKeySecret)
	}
	raw, err := json.Marshal(cfg.CLI)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "alarmd-cli-admin") || strings.Contains(string(raw), key) {
		t.Fatalf("CLI configuration evidence carries the Secret or the key: %s", raw)
	}
	if _, err := Load(writeConfig(t, validGoAccessRuntimeConfigYAML("cli-worker")+
		"cli:\n  admin_key_secret:\n    name: from-file\n")); err == nil {
		t.Fatal("a configuration file stated the Secret's names")
	}
}
