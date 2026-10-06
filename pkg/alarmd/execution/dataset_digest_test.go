package execution_test

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func digestTestRecords() []contract.CanonicalRecordV2 {
	build := func(digest string, sourceTime int64) contract.CanonicalRecordV2 {
		return contract.CanonicalRecordV2{
			RecordID: digest + "." + "1757462400", SourceTime: sourceTime, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{
				Fields: []contract.DimensionFieldV2{
					{Name: "bk_target_ip", Value: json.RawMessage(`"127.0.0.1"`)},
					{Name: "device_name", Value: json.RawMessage(`"cpu0"`)},
				},
				Digest: digest,
			},
			Values:       map[string]json.RawMessage{"_result_": json.RawMessage(`1`)},
			Dimensions:   map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"127.0.0.1"`)},
			ReceivedTime: sourceTime + 60,
		}
	}
	return []contract.CanonicalRecordV2{
		build("9f2a7c1e5b3d8f0a4c6e2b9d7f1a3c5e", 1757462400),
		build("3a7d2e0f5b8c1d4e7f0a3b6c9d2e5f8a", 1757462460),
		{RecordID: "no-identity", SourceTime: 1757462520, BusinessID: "2"},
	}
}

// TestDimensionIdentityDigestMatchesTheClonedIdentity proves the digest-only
// read answers exactly what the cloning read answers, for records with and
// without a dimension identity.
func TestDimensionIdentityDigestMatchesTheClonedIdentity(t *testing.T) {
	dataset := execution.NewDataset(digestTestRecords())
	for index := 0; index < dataset.Len(); index++ {
		record, ok := dataset.Record(index)
		if !ok {
			t.Fatalf("record %d is missing", index)
		}
		if got, want := record.DimensionIdentityDigest(), record.DimensionIdentity().Digest; got != want {
			t.Fatalf("record %d digest is %q, cloned identity says %q", index, got, want)
		}
	}
	var absent execution.RecordView
	if got := absent.DimensionIdentityDigest(); got != absent.DimensionIdentity().Digest {
		t.Fatalf("an absent record reports digest %q", got)
	}
}

// TestDimensionIdentityDigestExposesNothingWritable proves the cheap read
// keeps the Dataset immutable: it hands back a string, and the cloned identity
// a caller may still take remains a copy.
func TestDimensionIdentityDigestExposesNothingWritable(t *testing.T) {
	dataset := execution.NewDataset(digestTestRecords())
	record, ok := dataset.Record(0)
	if !ok {
		t.Fatal("record is missing")
	}
	before := record.DimensionIdentityDigest()
	identity := record.DimensionIdentity()
	identity.Digest = "overwritten"
	identity.Fields[0].Name = "overwritten"
	identity.Fields[0].Value[1] = 'X'
	if after := record.DimensionIdentityDigest(); after != before {
		t.Fatalf("digest moved from %q to %q after a caller wrote to its own copy", before, after)
	}
	fresh, _ := dataset.Record(0)
	if fresh.DimensionIdentity().Fields[0].Name != "bk_target_ip" {
		t.Fatal("a caller's copy reached the Dataset")
	}
}
