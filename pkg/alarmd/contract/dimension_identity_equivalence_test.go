// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package contract

import (
	"encoding/json"
	"fmt"
	"testing"
	"unicode/utf8"
)

// deriveDimensionIdentityDigestV2Frozen is the implementation as it stood
// before the per-field canonical form was removed, kept here verbatim so the
// optimisation is checked against the behaviour it replaces rather than
// against a table of digests somebody would have to regenerate.
//
// A frozen copy answers a question a golden table cannot: fuzz input. A table
// only covers the cases whoever wrote it thought of, and the rejection
// boundary is exactly where an unthought-of case would land.
func deriveDimensionIdentityDigestV2Frozen(tenantID, businessID string, fields []DimensionFieldV2) (string, error) {
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
		canonical, err := CanonicalJSONV2(dimension.Value)
		if err != nil || len(canonical) == 0 || canonical[0] == '{' || canonical[0] == '[' {
			return "", invalid("dimension_identity.fields.value", "must be a scalar or null JSON value")
		}
		previous = dimension.Name
	}
	canonicalFields, err := CanonicalJSONV2(fields)
	if err != nil {
		return "", invalid("dimension_identity.fields", err.Error())
	}
	return deriveLengthPrefixedSHA256(
		"dimension_identity.digest", "dimension-identity-v1",
		[]byte(tenantID), []byte(businessID), canonicalFields,
	)
}

// dimensionIdentityCorpus covers the rejection boundary the per-field call was
// standing on, not just the happy path: every reason CanonicalJSONV2 could
// have refused a value has an entry, because removing the call means each of
// those reasons now has to be refused by something else.
var dimensionIdentityCorpus = []struct {
	name  string
	value string
}{
	// Written with Go escape sequences only, never literal characters. The
	// difference is the point of several of these: a payload carrying the six
	// bytes \u2028 and one carrying that code point directly are different
	// inputs, and the encoder treats them differently on the way back out. An
	// earlier revision of this file held the literal, and because U+2028 ends a
	// line for the Go scanner it silently split the literal in two and tested
	// something else while still compiling.
	{"string", "\"plain\""},
	{"string with escapes", "\"a\\\"b\\\\c\\/d\\u00e9\""},
	{"string with unicode literal", "\"\u7ef4\u5ea6\u503c\""},
	{"string with line separator escape", "\"a\\u2028b\""},
	{"string with line separator literal", "\"a\u2028b\""},
	{"string with paragraph separator literal", "\"a\u2029b\""},
	{"string with lone high surrogate", "\"\\ud800\""},
	{"string with lone low surrogate", "\"\\udc00\""},
	{"string with surrogate pair escape", "\"\\ud83d\\ude00\""},
	{"string with astral literal", "\"\U0001f600\""},
	{"string with html characters", "\"a<b>c&d\""},
	{"string with control escape", "\"a\\u0000b\""},
	{"integer", `1`},
	{"negative integer", `-42`},
	{"large integer beyond float64", `9007199254740993`},
	{"integer with trailing zeros", `1.00`},
	{"exponent", `1e3`},
	{"float", `3.14159`},
	{"true", `true`},
	{"false", `false`},
	{"null", `null`},
	{"leading whitespace", "  \t\n\"x\""},
	{"trailing whitespace", "\"x\"  \n"},
	{"object", `{"a":1}`},
	{"object with duplicate keys", `{"a":1,"a":2}`},
	{"array", `[1,2]`},
	{"empty", ``},
	{"whitespace only", `   `},
	{"malformed", `{`},
	{"trailing value", `1 2`},
	{"bom prefixed", "\xef\xbb\xbf1"},
	{"invalid utf8", "\"\xff\""},
	{"nested object inside string", `"{\"a\":1}"`},
}

func TestDimensionIdentityDigestMatchesTheFrozenImplementation(t *testing.T) {
	// Single-field cases first: one field isolates which value was refused.
	for _, entry := range dimensionIdentityCorpus {
		t.Run(entry.name, func(t *testing.T) {
			fields := []DimensionFieldV2{{Name: "dim", Value: json.RawMessage(entry.value)}}
			assertDimensionIdentityAgrees(t, "tenant", "2", fields)
		})
	}
	// Then every pair, because a value that is refused on its own must still be
	// refused beside a valid one, and a value that is accepted must produce the
	// same digest whatever it sits next to.
	t.Run("pairs", func(t *testing.T) {
		for _, left := range dimensionIdentityCorpus {
			for _, right := range dimensionIdentityCorpus {
				fields := []DimensionFieldV2{
					{Name: "dim_a", Value: json.RawMessage(left.value)},
					{Name: "dim_b", Value: json.RawMessage(right.value)},
				}
				assertDimensionIdentityAgrees(t, "tenant", "2", fields)
			}
		}
	})
	// The guards that run before any value is looked at have to keep firing in
	// the same order, or an input refused for its tenant today would start
	// being refused for its value instead.
	t.Run("identity guards", func(t *testing.T) {
		valid := []DimensionFieldV2{{Name: "dim", Value: json.RawMessage(`"x"`)}}
		assertDimensionIdentityAgrees(t, "", "2", valid)
		assertDimensionIdentityAgrees(t, "\xff", "2", valid)
		assertDimensionIdentityAgrees(t, "tenant", "", valid)
		assertDimensionIdentityAgrees(t, "tenant", "007", valid)
		assertDimensionIdentityAgrees(t, "tenant", "2", nil)
		assertDimensionIdentityAgrees(t, "tenant", "2", []DimensionFieldV2{})
		assertDimensionIdentityAgrees(t, "tenant", "2", []DimensionFieldV2{{Name: "", Value: json.RawMessage(`1`)}})
		assertDimensionIdentityAgrees(t, "tenant", "2", []DimensionFieldV2{
			{Name: "b", Value: json.RawMessage(`1`)}, {Name: "a", Value: json.RawMessage(`1`)}})
		assertDimensionIdentityAgrees(t, "tenant", "2", []DimensionFieldV2{
			{Name: "a", Value: json.RawMessage(`1`)}, {Name: "a", Value: json.RawMessage(`1`)}})
	})
}

func assertDimensionIdentityAgrees(t *testing.T, tenantID, businessID string, fields []DimensionFieldV2) {
	t.Helper()
	wantDigest, wantErr := deriveDimensionIdentityDigestV2Frozen(tenantID, businessID, fields)
	gotDigest, gotErr := DeriveDimensionIdentityDigestV2(tenantID, businessID, fields)
	if (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("fields=%v: frozen err=%v, current err=%v; the rejection boundary moved", fields, wantErr, gotErr)
	}
	if wantDigest != gotDigest {
		t.Fatalf("fields=%v: frozen digest=%q, current digest=%q; the digest is not byte identical",
			fields, wantDigest, gotDigest)
	}
}

func FuzzDimensionIdentityDigestMatchesTheFrozenImplementation(f *testing.F) {
	for _, entry := range dimensionIdentityCorpus {
		f.Add(entry.value, entry.value)
	}
	f.Fuzz(func(t *testing.T, left, right string) {
		fields := []DimensionFieldV2{
			{Name: "dim_a", Value: json.RawMessage(left)},
			{Name: "dim_b", Value: json.RawMessage(right)},
		}
		wantDigest, wantErr := deriveDimensionIdentityDigestV2Frozen("tenant", "2", fields)
		gotDigest, gotErr := DeriveDimensionIdentityDigestV2("tenant", "2", fields)
		if (wantErr == nil) != (gotErr == nil) {
			t.Fatalf("left=%q right=%q: frozen err=%v, current err=%v", left, right, wantErr, gotErr)
		}
		if wantDigest != gotDigest {
			t.Fatalf("left=%q right=%q: frozen digest=%q, current digest=%q", left, right, wantDigest, gotDigest)
		}
	})
}

// Both arms in one binary and one run, alternating, because the two numbers
// are only comparable if they met the same machine. Measured separately they
// would carry whatever else was running at the time, and this machine runs
// several other things.
func BenchmarkDimensionIdentityFrozenVersusCurrent(b *testing.B) {
	for _, fields := range []int{1, 4, 8, 16} {
		input := benchmarkDimensionFields(fields)
		b.Run(fmt.Sprintf("frozen/fields=%d", fields), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := deriveDimensionIdentityDigestV2Frozen("tenant", "2", input); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("current/fields=%d", fields), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := DeriveDimensionIdentityDigestV2("tenant", "2", input); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
