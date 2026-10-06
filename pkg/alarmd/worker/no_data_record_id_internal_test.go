package worker

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A synthetic no-data record carries the record id its source time derives,
// the same way a record that arrived through the reader does.
//
// The platform's rule is that a record id is DeriveRecordIDV2 of the dimension
// identity digest and the source time, and the reader refuses any record where
// it is not (contract/v2_reader.go). A synthetic record is built rather than
// read, so nothing enforced it here, and this one used to carry the dimension
// digest itself - the same value for every source time of the series.
//
// The consequence is not local. Within one Runtime State key the series digest
// is fixed, so for every other series the record id is a function of the source
// time alone, and decision-021 stores no id per point because of it. These
// series were the one exception, which would have made the derived id disagree
// with the stored one on every no-data point - and the write-side check that
// catches a producer skipping the derivation would then refuse every no-data
// write there is.
//
// Asserted as "equals the derivation" rather than "is not the digest": the
// second passes for any change at all, including one that breaks the rule a
// different way.
func TestANoDataRecordCarriesTheIDItsSourceTimeDerives(t *testing.T) {
	due := noDataWiredPlan(t)
	stream := noDataWiredStream(t, due, &emptyNoDataStore{})
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	round, err := stream.noDataRoundFor(due, nil, execution.CompletenessFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(round.series) != 1 {
		t.Fatalf("series = %d, want the whole item judged once", len(round.series))
	}
	entry := round.series[0]
	record, ok := entry.inputs[0].Inputs[0].View.Record(0)
	if !ok {
		t.Fatal("the synthetic binding carries no record")
	}

	digest := record.DimensionIdentityDigest()
	if digest == "" {
		t.Fatal("the synthetic record has no dimension identity digest to derive from")
	}
	want, err := contract.DeriveRecordIDV2(digest, record.SourceTime())
	if err != nil {
		t.Fatalf("derive the expected id: %v", err)
	}
	if got := record.RecordID(); got != want {
		t.Fatalf("record id is %s, want %s (DeriveRecordIDV2 of the dimension digest and source time %d); "+
			"a synthetic record that skips the derivation makes the record id something other than a "+
			"function of the source time within its state key", got, want, record.SourceTime())
	}
	// The series identity stays the dimension digest: the key is per series and
	// the id is per point, and collapsing them is what this change undoes.
	if string(entry.series) != digest {
		t.Fatalf("series identity is %q, want the dimension digest %q", entry.series, digest)
	}
	if got := record.RecordID(); got == digest {
		t.Fatal("the record id still equals the dimension digest, so it is constant across source times")
	}
}
