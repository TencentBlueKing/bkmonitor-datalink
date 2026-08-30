package execution

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestRecordViewValueIsReadOnly(t *testing.T) {
	dataset := NewDataset([]contract.CanonicalRecordV2{{Values: map[string]json.RawMessage{"value": json.RawMessage(`12`)}}})
	record, _ := dataset.Record(0)
	value, ok := record.Value("value")
	if !ok || string(value) != "12" {
		t.Fatalf("Value() = %q, %v", value, ok)
	}
	value[0] = '9'
	again, _ := record.Value("value")
	if string(again) != "12" {
		t.Fatalf("Value() leaked writable bytes: %q", again)
	}
	if _, ok := record.Value("missing"); ok {
		t.Fatal("Value() found missing field")
	}
}
