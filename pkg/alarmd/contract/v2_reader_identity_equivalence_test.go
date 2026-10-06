// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package contract

import (
	"encoding/json"
	"testing"
	"unicode/utf8"
)

// deriveDimensionIdentityDigestPrevalidatedV2Frozen is the reader's deriver as
// it stood before the wasted canonicalisation was removed, kept verbatim.
//
// The writer's copy of this defect was removed in e9be4cf2 and proved the same
// way. The reader's copy survived because nobody looked for the pattern's
// siblings, only at the instance that had been reported.
func deriveDimensionIdentityDigestPrevalidatedV2Frozen(tenantID, businessID string, fields []DimensionFieldV2) (string, error) {
	if tenantID == "" || !utf8.ValidString(tenantID) {
		return "", invalid("dimension_identity.tenant_id", "must be non-empty valid UTF-8")
	}
	if !canonicalSignedDecimalPattern.MatchString(businessID) {
		return "", invalid("dimension_identity.business_id", "must use canonical signed decimal form")
	}
	if fields == nil {
		return "", invalid("dimension_identity.fields", "must be an array")
	}
	previous := ""
	for index, dimension := range fields {
		if dimension.Name == "" || !utf8.ValidString(dimension.Name) || (index > 0 && dimension.Name <= previous) {
			return "", invalid("dimension_identity.fields", "names must be non-empty, sorted and unique")
		}
		canonical, err := canonicalJSONPrevalidatedV2(dimension.Value)
		if err != nil || len(canonical) == 0 || canonical[0] == '{' || canonical[0] == '[' {
			return "", invalid("dimension_identity.fields.value", "must be a scalar or null JSON value")
		}
		previous = dimension.Name
	}
	canonicalFields, err := canonicalJSONPrevalidatedV2(fields)
	if err != nil {
		return "", invalid("dimension_identity.fields", err.Error())
	}
	return deriveLengthPrefixedSHA256(
		"dimension_identity.digest", "dimension-identity-v1",
		[]byte(tenantID), []byte(businessID), canonicalFields,
	)
}

func assertPrevalidatedIdentityAgrees(t *testing.T, tenantID, businessID string, fields []DimensionFieldV2) {
	t.Helper()
	wantDigest, wantErr := deriveDimensionIdentityDigestPrevalidatedV2Frozen(tenantID, businessID, fields)
	gotDigest, gotErr := deriveDimensionIdentityDigestPrevalidatedV2(tenantID, businessID, fields)
	if (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("fields=%v: frozen err=%v, current err=%v; the rejection boundary moved",
			fields, wantErr, gotErr)
	}
	if wantDigest != gotDigest {
		t.Fatalf("fields=%v: frozen digest=%q, current digest=%q; this deriver validates persisted identity",
			fields, wantDigest, gotDigest)
	}
}

// The same corpus the writer's equivalence used, so both copies of the pattern
// are held to one standard rather than each to a list somebody wrote for it.
func TestPrevalidatedIdentityDigestMatchesTheFrozenImplementation(t *testing.T) {
	for _, entry := range dimensionIdentityCorpus {
		t.Run(entry.name, func(t *testing.T) {
			assertPrevalidatedIdentityAgrees(t, "tenant", "2",
				[]DimensionFieldV2{{Name: "dim", Value: json.RawMessage(entry.value)}})
		})
	}
	t.Run("pairs", func(t *testing.T) {
		for _, left := range dimensionIdentityCorpus {
			for _, right := range dimensionIdentityCorpus {
				assertPrevalidatedIdentityAgrees(t, "tenant", "2", []DimensionFieldV2{
					{Name: "dim_a", Value: json.RawMessage(left.value)},
					{Name: "dim_b", Value: json.RawMessage(right.value)},
				})
			}
		}
	})
	t.Run("identity guards", func(t *testing.T) {
		valid := []DimensionFieldV2{{Name: "dim", Value: json.RawMessage(`"x"`)}}
		assertPrevalidatedIdentityAgrees(t, "", "2", valid)
		assertPrevalidatedIdentityAgrees(t, "\xff", "2", valid)
		assertPrevalidatedIdentityAgrees(t, "tenant", "", valid)
		assertPrevalidatedIdentityAgrees(t, "tenant", "007", valid)
		assertPrevalidatedIdentityAgrees(t, "tenant", "2", nil)
		assertPrevalidatedIdentityAgrees(t, "tenant", "2", []DimensionFieldV2{})
		assertPrevalidatedIdentityAgrees(t, "tenant", "2",
			[]DimensionFieldV2{{Name: "b", Value: json.RawMessage(`1`)}, {Name: "a", Value: json.RawMessage(`1`)}})
	})
}

func FuzzPrevalidatedIdentityDigestMatchesTheFrozenImplementation(f *testing.F) {
	for _, entry := range dimensionIdentityCorpus {
		f.Add(entry.value, entry.value)
	}
	f.Fuzz(func(t *testing.T, left, right string) {
		assertPrevalidatedIdentityAgrees(t, "tenant", "2", []DimensionFieldV2{
			{Name: "dim_a", Value: json.RawMessage(left)},
			{Name: "dim_b", Value: json.RawMessage(right)},
		})
	})
}
