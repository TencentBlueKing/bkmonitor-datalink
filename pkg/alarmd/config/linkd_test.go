package config

import "testing"

func TestLinkdCapacityTracksContainer(t *testing.T) {
	small := DeriveLinkdCapacity(CapacityInputs{CPUBudget: 2, MemoryLimitBytes: 2 << 30})
	large := DeriveLinkdCapacity(CapacityInputs{CPUBudget: 8, MemoryLimitBytes: 8 << 30})
	if large.Bytes < small.Bytes*3 || large.Members < small.Members*3 || large.ReadBatch <= small.ReadBatch {
		t.Fatalf("capacity did not scale: %+v %+v", small, large)
	}
	if large.Bytes != (8<<30)/16 {
		t.Fatalf("linkd bytes = %d, want a sixteenth of the container", large.Bytes)
	}
	// A 512Mi container holds 64k alerts and 16k strategies.
	if small := DeriveLinkdCapacity(CapacityInputs{CPUBudget: 1, MemoryLimitBytes: 512 << 20}); small.Members < 64<<10 || small.Strategies < 16<<10 {
		t.Fatalf("512Mi holds %d alerts and %d strategies, want at least 64Ki and 16Ki", small.Members, small.Strategies)
	}
}

// A close is a Kafka write, not CPU work: a step closes a thousand at any
// CPU budget.
func TestACloseStepIsAThousandAtAnyCPU(t *testing.T) {
	for _, cpu := range []int{1, 2, 8} {
		if got := DeriveLinkdCapacity(CapacityInputs{CPUBudget: cpu, MemoryLimitBytes: 512 << 20}).CloseBatch; got != 1000 {
			t.Fatalf("CPU %d: close batch = %d, want 1000", cpu, got)
		}
	}
}

// The calendar cache has its own share of memory, a sixty-fourth, and does
// not borrow linkd's.
func TestTheCalendarCacheHasItsOwnShare(t *testing.T) {
	entries, bytes := DeriveCalendarCacheCapacity(CapacityInputs{CPUBudget: 1, MemoryLimitBytes: 512 << 20})
	if bytes != (512<<20)/64 || entries != bytes/2048 {
		t.Fatalf("calendar cache = %d entries, %d bytes; want %d bytes and a 2 KiB entry", entries, bytes, (512<<20)/64)
	}
}

func TestLinkdConfigurationBinding(t *testing.T) {
	if err := (LinkdConfig{}).Validate(); err != nil {
		t.Fatal(err)
	}
	// The target is read from the Console, so a Console with credentials
	// is the whole configuration.
	c := LinkdConfig{ConsoleURL: "https://console.example.test/local-api", Username: "reader", Password: "test-password"}
	if err := c.Validate(); err != nil {
		t.Fatalf("a Console with credentials alone was refused: %v", err)
	}
	c.ConsoleURL = "https://reader:password@console.example.test/local-api"
	if err := c.Validate(); err == nil {
		t.Fatal("URL credentials accepted")
	}
	c.ConsoleURL = "https://console.example.test/local-api"
	c.Password = ""
	if err := c.Validate(); err == nil {
		t.Fatal("half a Basic pair was accepted")
	}
	// No pair at all is the admin's API, called with the token it hands over.
	if err := (LinkdConfig{ConsoleURL: "http://admin.example.test/api/modules/linkd"}).Validate(); err != nil {
		t.Fatalf("the admin's API root without a Basic pair was refused: %v", err)
	}
	if err := (LinkdConfig{ConsoleURL: "http://admin.example.test/"}).Validate(); err == nil {
		t.Fatal("a server URL that is not an API root was accepted")
	}
	if err := (LinkdConfig{Username: "reader"}).Validate(); err == nil {
		t.Fatal("credentials without a Console were accepted")
	}
}

// The Console credentials may come from the environment, which is how a
// deployment hands alarmd the alert link's own Console Secret. Stated in both
// places, they are refused rather than one silently winning.
func TestLinkdConsoleCredentialsComeFromTheEnvironment(t *testing.T) {
	t.Setenv(LinkdConsoleUsernameEnvironment, "reader")
	t.Setenv(LinkdConsolePasswordEnvironment, "test-password")
	c := LinkdConfig{ConsoleURL: "https://console.example.test/local-api"}
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
		"  linkd:\n    console_url: https://console.example.test/local-api\n"
	cfg, err := LoadWithRoles(writeConfig(t, text), nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PhaseTwo.Linkd.Username != "reader" || cfg.PhaseTwo.Linkd.Password != "test-password" {
		t.Fatalf("loading did not take the credentials from the environment: %+v", cfg.PhaseTwo.Linkd)
	}
}
