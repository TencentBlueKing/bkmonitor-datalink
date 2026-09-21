// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import (
	"strings"
	"testing"
)

// Every field the matcher can be asked about is a row in the table, and a
// row says where the candidates come from and what their absence does. A
// field with no row is not "matches nothing" - it is a validation failure,
// so a scope naming one never reaches a Plan.
func TestEveryTargetScopeFieldIsATableRow(t *testing.T) {
	want := map[TargetScopeField]TargetScopeAttribute{
		TargetScopeHost: {
			Attribute: AttributeHostIdentity, Source: TargetScopeSourceFacts,
			Absence: TargetScopeAbsenceSkipCondition, MismatchReason: TargetScopeReasonOutOfScope,
		},
		TargetScopeServiceInstance: {
			Attribute: AttributeServiceInstanceID, Source: TargetScopeSourceFacts,
			Absence: TargetScopeAbsenceSkipCondition, MismatchReason: TargetScopeReasonOutOfScope,
		},
		TargetScopeTopoNode: {
			Attribute: AttributeHostTopoNode, Source: TargetScopeSourceFacts,
			Absence: TargetScopeAbsenceFailGroup, AbsenceReason: TargetScopeReasonOutOfScope,
			MismatchReason: TargetScopeReasonOutOfScope,
		},
		TargetScopeObjectModelInst: {
			Source:  TargetScopeSourceDimensionPairs,
			Absence: TargetScopeAbsenceFailGroup, AbsenceReason: TargetScopeReasonObjectIdentityMissing,
			MismatchReason: TargetScopeReasonObjectIdentityUnmatched,
		},
	}
	fields := TargetScopeFields()
	if len(fields) != len(want) {
		t.Fatalf("table has %d rows %v, want %d", len(fields), fields, len(want))
	}
	for field, expected := range want {
		got, known := TargetScopeAttributeFor(field)
		if !known {
			t.Fatalf("field %s has no row", field)
		}
		expected.Field = field
		if got != expected {
			t.Fatalf("row for %s = %+v, want %+v", field, got, expected)
		}
	}
	if _, known := TargetScopeAttributeFor("DYNAMIC_GROUP"); known {
		t.Fatal("a field the compiler refuses has a row")
	}
	// A skip-on-absence field cannot name an absence reason: a skipped
	// condition never ends a group, so there is nothing to report.
	for _, field := range fields {
		row, _ := TargetScopeAttributeFor(field)
		if row.Absence == TargetScopeAbsenceSkipCondition && row.AbsenceReason != "" {
			t.Fatalf("%s skips on absence yet names an absence reason %q", field, row.AbsenceReason)
		}
		if row.Absence == TargetScopeAbsenceFailGroup && row.AbsenceReason == "" {
			t.Fatalf("%s fails the group on absence yet names no reason for it", field)
		}
		if row.MismatchReason == "" {
			t.Fatalf("%s names no mismatch reason", field)
		}
	}
}

func objectCondition(keys []string, pairs ...[2]string) TargetScopeConditionV2 {
	return TargetScopeConditionV2{
		Field: TargetScopeObjectModelInst, Method: TargetScopeInclude, Keys: keys, IdentityFields: pairs,
	}
}

// The identity pairs are the strategy's own fact about how its records are
// keyed. They belong to the object-model field and only to it: on any other
// field they are a claim the matcher would never read, which is the kind of
// silent field this contract refuses.
func TestIdentityPairsBelongToTheObjectModelFieldOnly(t *testing.T) {
	valid := &TargetScopeV2{Groups: []TargetScopeGroupV2{{Conditions: []TargetScopeConditionV2{
		objectCondition([]string{"host|12", "switch|3"}, [2]string{"cw_object_model_id", "cw_object_model_inst_id"}),
	}}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a well-formed object-model condition failed validation: %v", err)
	}

	for name, scope := range map[string]*TargetScopeV2{
		"no pairs": {Groups: []TargetScopeGroupV2{{Conditions: []TargetScopeConditionV2{
			objectCondition([]string{"host|12"}),
		}}}},
		"half a pair": {Groups: []TargetScopeGroupV2{{Conditions: []TargetScopeConditionV2{
			objectCondition([]string{"host|12"}, [2]string{"cw_object_model_id", ""}),
		}}}},
		"duplicate pair": {Groups: []TargetScopeGroupV2{{Conditions: []TargetScopeConditionV2{
			objectCondition([]string{"host|12"}, [2]string{"a", "b"}, [2]string{"a", "b"}),
		}}}},
		"pairs on a topology field": {Groups: []TargetScopeGroupV2{{Conditions: []TargetScopeConditionV2{{
			Field: TargetScopeTopoNode, Method: TargetScopeInclude, Keys: []string{"set|1"},
			IdentityFields: [][2]string{{"a", "b"}},
		}}}}},
		"unknown field": {Groups: []TargetScopeGroupV2{{Conditions: []TargetScopeConditionV2{{
			Field: "HOST_ATTR_OS", Method: TargetScopeInclude, Keys: []string{"linux"},
		}}}}},
	} {
		if err := scope.Validate(); err == nil {
			t.Fatalf("%s validated", name)
		}
	}
}

// A scope compiled before the object-model field existed must digest to the
// same bytes it did then, or every retained Plan is a "changed" Plan on the
// release that adds the field. The pairs are omitted from the wire when
// absent, which is what keeps the bytes identical.
func TestScopesWithoutIdentityPairsEncodeAsBefore(t *testing.T) {
	scope := &TargetScopeV2{Groups: []TargetScopeGroupV2{{Conditions: []TargetScopeConditionV2{{
		Field: TargetScopeTopoNode, Method: TargetScopeInclude, Keys: []string{"set|81"},
	}}}}}
	encoded, err := CanonicalJSONV2(scope)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(string(encoded), "identity_fields") {
		t.Fatalf("a scope without pairs carries the field on the wire: %s", encoded)
	}
	if string(encoded) != `{"groups":[{"conditions":[{"field":"TOPO_NODE","keys":["set|81"],"method":"EQ"}]}]}` {
		t.Fatalf("wire form changed: %s", encoded)
	}
	withPairs := &TargetScopeV2{Groups: []TargetScopeGroupV2{{Conditions: []TargetScopeConditionV2{
		objectCondition([]string{"host|12"}, [2]string{"cw_object_model_id", "cw_object_model_inst_id"}),
	}}}}
	encoded, err = CanonicalJSONV2(withPairs)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(string(encoded), `"identity_fields":[["cw_object_model_id","cw_object_model_inst_id"]]`) {
		t.Fatalf("pairs did not reach the wire: %s", encoded)
	}
}
