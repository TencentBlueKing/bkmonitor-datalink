package state

import (
	"strconv"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func memoTestIdentity() execution.StateKeyIdentity {
	return execution.StateKeyIdentity{
		Plan:                 execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "104857"},
		StateGeneration:      execution.StateGeneration("2f6c1d9e4a7b0c3d5e8f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d"),
		SeriesIdentityDigest: execution.SeriesIdentityDigest("9f2a7c1e5b3d8f0a4c6e2b9d7f1a3c5e7b9d1f3a5c7e9b1d3f5a7c9e1b3d5f7a"),
	}
}

// TestRuntimeStateKeyIsUnchangedByTheSegmentMemo derives the key segments
// directly and rebuilds the key by hand, so a memo that ever answered with a
// segment other than the derived one would move stored state to a key nothing
// reads. It is checked against a literal too, which catches the same move made
// deliberately in the derivation itself.
func TestRuntimeStateKeyIsUnchangedByTheSegmentMemo(t *testing.T) {
	identity := memoTestIdentity()
	tenant, err := contract.DeriveCanonicalDigestV2("alarmd-runtime-tenant-v2", identity.Plan.TenantID)
	if err != nil {
		t.Fatalf("derive tenant digest: %v", err)
	}
	generation, err := contract.DeriveCanonicalDigestV2("alarmd-state-generation-v2", string(identity.StateGeneration))
	if err != nil {
		t.Fatalf("derive generation digest: %v", err)
	}
	series, err := contract.DeriveCanonicalDigestV2("alarmd-runtime-series-v2", identity.SeriesIdentityDigest)
	if err != nil {
		t.Fatalf("derive series digest: %v", err)
	}
	expected := strings.Join([]string{
		"bkmonitor:alarmd", "runtime", "v2", tenant[:32],
		identity.Plan.BusinessID, identity.Plan.StrategyID, generation[:32], series[:32],
	}, ":")

	// Derived many times so both the first miss and later hits are covered.
	for attempt := 0; attempt < 3; attempt++ {
		key, err := RuntimeStateKeyV2("bkmonitor:alarmd", identity)
		if err != nil {
			t.Fatalf("runtime state key: %v", err)
		}
		if key != expected {
			t.Fatalf("runtime state key changed:\n got %s\nwant %s", key, expected)
		}
	}
	const pinned = "bkmonitor:alarmd:runtime:v2:77678a44bdef4cc8e4d15adb81944d1f:2:104857:28682c24e924a0b91cc4d92e21770244:0b5ef30c19994e74e9624e70eba9ceb9"
	key, err := RuntimeStateKeyV2("bkmonitor:alarmd", identity)
	if err != nil {
		t.Fatalf("runtime state key: %v", err)
	}
	if key != pinned {
		t.Fatalf("runtime state key changed:\n got %s\nwant %s", key, pinned)
	}
}

// TestKeySegmentMemoSeparatesDomainsAndValues proves the memo answers per
// domain and per value: two identities that differ only in tenant or only in
// state generation must not share a key.
func TestKeySegmentMemoSeparatesDomainsAndValues(t *testing.T) {
	base := memoTestIdentity()
	baseKey, err := RuntimeStateKeyV2("bkmonitor:alarmd", base)
	if err != nil {
		t.Fatalf("base key: %v", err)
	}
	otherTenant := memoTestIdentity()
	otherTenant.Plan.TenantID = "tenant-two"
	otherTenantKey, err := RuntimeStateKeyV2("bkmonitor:alarmd", otherTenant)
	if err != nil {
		t.Fatalf("other tenant key: %v", err)
	}
	if otherTenantKey == baseKey {
		t.Fatal("two tenants share one runtime state key")
	}
	otherGeneration := memoTestIdentity()
	otherGeneration.StateGeneration = execution.StateGeneration("3a7d2e0f5b8c1d4e7f0a3b6c9d2e5f8a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e")
	otherGenerationKey, err := RuntimeStateKeyV2("bkmonitor:alarmd", otherGeneration)
	if err != nil {
		t.Fatalf("other generation key: %v", err)
	}
	if otherGenerationKey == baseKey {
		t.Fatal("two state generations share one runtime state key")
	}
	// The same value under the two domains must not be answered from one memo.
	shared := "shared-identity-value"
	if tenantKeySegments.digest(shared) == generationKeySegments.digest(shared) {
		t.Fatal("tenant and state generation digests of one value are equal")
	}
}

// TestKeySegmentMemoStartsOverAtItsBound proves the bound is enforced and that
// a cleared memo still answers with the derived digest.
func TestKeySegmentMemoStartsOverAtItsBound(t *testing.T) {
	memo := newKeySegmentMemo("alarmd-runtime-tenant-v2")
	for index := 0; index < keySegmentMemoEntries+16; index++ {
		value := "tenant-" + strings.Repeat("x", index%7) + string(rune('a'+index%26)) + strconv.Itoa(index)
		expected, err := contract.DeriveCanonicalDigestV2("alarmd-runtime-tenant-v2", value)
		if err != nil {
			t.Fatalf("derive %q: %v", value, err)
		}
		if got := memo.digest(value); got != expected {
			t.Fatalf("memo answered %q for %q, derivation gives %q", got, value, expected)
		}
	}
	memo.mu.RLock()
	held := len(memo.digests)
	memo.mu.RUnlock()
	if held > keySegmentMemoEntries {
		t.Fatalf("memo holds %d entries, bound is %d", held, keySegmentMemoEntries)
	}
}
