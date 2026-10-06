package config

import "testing"

func TestLinkdCapacityTracksContainer(t *testing.T) {
	small := DeriveLinkdCapacity(CapacityInputs{CPUBudget: 2, MemoryLimitBytes: 2 << 30})
	large := DeriveLinkdCapacity(CapacityInputs{CPUBudget: 8, MemoryLimitBytes: 8 << 30})
	if large.Bytes < small.Bytes*3 || large.Members < small.Members*3 || large.ReadBatch <= small.ReadBatch {
		t.Fatalf("capacity did not scale: %+v %+v", small, large)
	}
	if large.Bytes > (8<<30)/100 {
		t.Fatal("exceeded container share")
	}
}

func TestLinkdConfigurationBinding(t *testing.T) {
	if err := (LinkdConfig{}).Validate(); err != nil {
		t.Fatal(err)
	}
	c := LinkdConfig{ConsoleURL: "https://console.example.test", EventSourceID: "native-events", HookName: "active-index", Username: "reader", Password: "test-password"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.ConsoleURL = "https://reader:password@console.example.test"
	if err := c.Validate(); err == nil {
		t.Fatal("URL credentials accepted")
	}
	// The target is read from the Console; source and hook only narrow the
	// choice, so a Console with credentials alone is a whole configuration.
	c = LinkdConfig{ConsoleURL: "https://console.example.test", Username: "reader", Password: "test-password"}
	if err := c.Validate(); err != nil {
		t.Fatalf("a Console with credentials alone was refused: %v", err)
	}
	c.Password = ""
	if err := c.Validate(); err == nil {
		t.Fatal("a Console without credentials was accepted")
	}
}

// The Console credentials may come from the environment, which is how a
// deployment hands alarmd the alert link's own Console Secret. Stated in both
// places, they are refused rather than one silently winning.
func TestLinkdConsoleCredentialsComeFromTheEnvironment(t *testing.T) {
	t.Setenv(LinkdConsoleUsernameEnvironment, "reader")
	t.Setenv(LinkdConsolePasswordEnvironment, "test-password")
	c := LinkdConfig{ConsoleURL: "https://console.example.test", EventSourceID: "native-events", HookName: "active-index"}
	if err := c.resolveCredentialsFromEnvironment(); err != nil {
		t.Fatal(err)
	}
	if c.Username != "reader" || c.Password != "test-password" {
		t.Fatalf("credentials not taken from the environment: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	both := LinkdConfig{Password: "file-password"}
	if err := both.resolveCredentialsFromEnvironment(); err == nil {
		t.Fatal("a password stated in the file and the environment was accepted")
	}
	t.Setenv(LinkdConsoleUsernameEnvironment, "")
	t.Setenv(LinkdConsolePasswordEnvironment, "")
	fileOnly := LinkdConfig{Username: "file-reader", Password: "file-password"}
	if err := fileOnly.resolveCredentialsFromEnvironment(); err != nil || fileOnly.Username != "file-reader" {
		t.Fatalf("an empty environment overrode the file: %+v %v", fileOnly, err)
	}
}

// Loading applies it: a file that names the Console but not its credentials
// loads with the credentials the environment carries.
func TestLoadingTakesTheConsoleCredentialsFromTheEnvironment(t *testing.T) {
	t.Setenv(LinkdConsoleUsernameEnvironment, "reader")
	t.Setenv(LinkdConsolePasswordEnvironment, "test-password")
	text := validGoAccessRuntimeConfigYAML("linkd-worker") +
		"  linkd:\n    console_url: https://console.example.test\n    event_source_id: native-events\n    hook_name: active-index\n"
	cfg, err := Load(writeConfig(t, text))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PhaseTwo.Linkd.Username != "reader" || cfg.PhaseTwo.Linkd.Password != "test-password" {
		t.Fatalf("loading did not take the credentials from the environment: %+v", cfg.PhaseTwo.Linkd)
	}
}
