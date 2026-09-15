package shadow_test

import (
	"bytes"
	"encoding/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/shadow"
	"testing"
)

func TestPreparedEvidencePreservesV2AndRejectsOtherCall(t *testing.T) {
	input := frozenFinalInput(t, true)
	cfg, err := shadow.BuildFrozenComparisonConfigV2(input.Due, input.Requirements, input.Queries)
	if err != nil {
		t.Fatal(err)
	}
	input = frozenFinalFrom(t, input.Due, input.Requirements, input.Queries, func(event *contract.TriggerEventV1) {
		for i := range event.LevelResults {
			for _, level := range cfg.Levels {
				if level.LevelID == event.LevelResults[i].LevelID {
					window := &event.LevelResults[i].DecisionWindow.Trigger
					window.WindowEnd = event.RecordRef.SourceTime
					window.WindowStart = window.WindowEnd - int64(level.Trigger.StepSeconds)*int64(level.Trigger.WindowPoints) + 1
				}
			}
		}
	})
	prepared, err := shadow.PrepareFrozenEvidence(input.Due, input.Requirements, input.Queries, input.Frozen)
	if err != nil {
		t.Fatal(err)
	}
	original, err := shadow.EncodeGoFinalEvidenceV2(input, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := prepared.EncodeFinal(input, 1<<20)
	if err != nil || !bytes.Equal(original.CopyBytes(), encoded.CopyBytes()) {
		t.Fatal("V2 wire changed", err)
	}
	before, err := prepared.Business(input, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	originalBusiness, err := shadow.BuildGoBusinessAbnormal(input, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(originalBusiness)
	got, _ := json.Marshal(before)
	if !bytes.Equal(want, got) {
		t.Fatal("V2 business wire changed")
	}
	before.Config[0] = 'x'
	before.Primary.Values["value"] = "900"
	after, err := prepared.Business(input, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = json.Marshal(after)
	if !bytes.Equal(want, got) {
		t.Fatal("caller mutated prepared config")
	}
	changed := input
	changed.Frozen.QueryRevision = "other"
	if _, err = prepared.EncodeFinal(changed, 1<<20); err == nil {
		t.Fatal("other frozen call accepted")
	}
	changed = input
	changed.ACK.Confirmed = false
	if _, err = prepared.EncodeFinal(changed, 1<<20); err == nil {
		t.Fatal("cached config substituted ACK")
	}
	var zero shadow.PreparedFrozenEvidence
	if _, err = zero.EncodeFinal(input, 1<<20); err == nil {
		t.Fatal("zero prepared accepted")
	}
}
