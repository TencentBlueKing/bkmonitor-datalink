package contract_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestProcPortConfigNumericBranch(t *testing.T) {
	b, err := os.ReadFile("testdata/shadow-final-v2/frozen_expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	for _, entry := range c["levels"].([]any) {
		entry.(map[string]any)["detectors"] = []any{map[string]any{
			"kind": "ProcPort", "mapping_version": "canonical-proc-port-v1", "source_algorithm_family": "proc_port", "source_mapping_version": "python-proc-port-three-branches-v1",
			"semantic_config": map[string]any{"value_field": "value", "source_metric": "proc_exists", "nonlisten_field": "nonlisten", "not_accurate_listen_field": "not_accurate_listen", "bind_ip_field": "bind_ip", "value_mapping": "native-proc-port-result-v1"},
		}}
	}
	c["numeric"] = map[string]any{"source_unit": "", "target_unit": "", "multiplier": "1"}
	c["projection"] = map[string]any{"value_fields": []string{"value"}, "identity_fields": []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}, "required_dimensions": []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"}}
	b, _ = json.Marshal(c)
	decoded, err := contract.DecodeComparisonConfigV2(b, len(b))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = contract.CanonicalComparisonConfigV2(*decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"decimal_places", "rounding", "unknown"} {
		n := c["numeric"].(map[string]any)
		n[field] = 0
		bad, _ := json.Marshal(c)
		if _, err := contract.DecodeComparisonConfigV2(bad, len(bad)); err == nil {
			t.Fatalf("accepted %s", field)
		}
		delete(n, field)
	}
	for _, kind := range []string{"mapping", "source", "scalar", "semantic_missing", "semantic_extra", "unit_null", "dimension_identity"} {
		t.Run(kind, func(t *testing.T) {
			var bad map[string]any
			if err := json.Unmarshal(b, &bad); err != nil {
				t.Fatal(err)
			}
			detector := bad["levels"].([]any)[0].(map[string]any)["detectors"].([]any)[0].(map[string]any)
			switch kind {
			case "mapping":
				detector["mapping_version"] = "canonical-proc-port-v2"
			case "source":
				detector["source_mapping_version"] = "unknown"
			case "scalar":
				detector["operator"] = "NE"
			case "semantic_missing":
				delete(detector["semantic_config"].(map[string]any), "bind_ip_field")
			case "semantic_extra":
				detector["semantic_config"].(map[string]any)["unknown"] = "value"
			case "unit_null":
				bad["numeric"].(map[string]any)["target_unit"] = nil
			case "dimension_identity":
				bad["projection"].(map[string]any)["identity_fields"] = []string{"nonlisten"}
			}
			payload, _ := json.Marshal(bad)
			if _, err := contract.DecodeComparisonConfigV2(payload, len(payload)); err == nil {
				t.Fatal("accepted unsupported ProcPort contract")
			}
		})
	}

}
