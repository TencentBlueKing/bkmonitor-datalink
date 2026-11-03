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
	c := LinkdConfig{ControlPlane: LinkdControlPlaneConfig{URL: "https://linkd.example.test"}}
	if err := c.Validate(); err != nil {
		t.Fatalf("a control plane URL was refused: %v", err)
	}
	c.ControlPlane.URL = "https://user:password@linkd.example.test"
	if err := c.Validate(); err == nil {
		t.Fatal("URL credentials accepted")
	}
	if err := (LinkdConfig{ControlPlane: LinkdControlPlaneConfig{InlineJWTSecret: "x"}}).Validate(); err == nil {
		t.Fatal("an inline secret was accepted")
	}
}
