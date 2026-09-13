// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package contract

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// esc builds a JSON escape sequence without the sequence appearing literally
// in this file, and lit builds the character one denotes.
//
// Both exist for the same reason: two earlier revisions of these tests wrote
// the sequence directly and a tool folded it into the character. Once that
// silently split a string literal in two, once it put a raw NUL in the source.
// Neither failed loudly, and the first one still compiled and passed while
// testing something other than what was written.
func esc(hex string) string     { return "\\u" + hex }
func lit(codepoint rune) string { return string(codepoint) }

// Run with CANONICAL_BRANCH_DISCOVERY=1 to print what the current
// implementation actually does with each candidate branch input. The printed
// output is the source the branch table is written from.
//
// It exists because the table must be enumerated from the behaviour being
// replaced - not from the replacement, and not from what the documentation for
// encoding/json says. The contract names rules; one Go version implements
// them, and the two can differ at a boundary.
// canonicalBranchProbe is one branch of the canonical form together with an
// input that reaches it. The two travel together on purpose: a branch nobody
// can construct an input for is not a branch, it is a phantom, and leaving it
// in the denominator makes coverage permanently short of a target that does
// not exist.
type canonicalBranchProbe struct {
	name  string
	value any
}

// canonicalBranchProbes is the coverage denominator for replacing
// CanonicalJSONV2. It is enumerated from the behaviour being replaced - the
// contract plus what this Go version's encoder actually does - and never from
// the replacement, or the replacement would be setting its own exam.
func canonicalBranchProbes() []canonicalBranchProbe {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	q := func(inner string) json.RawMessage { return json.RawMessage(`"` + inner + `"`) }
	return []canonicalBranchProbe{
		// value shapes
		{"null", raw(`null`)},
		{"true", raw(`true`)},
		{"false", raw(`false`)},
		{"int", raw(`1`)},
		{"negative", raw(`-42`)},
		{"minus zero", raw(`-0`)},
		{"trailing zeros", raw(`1.00`)},
		{"exponent lower", raw(`1e3`)},
		{"exponent upper signed", raw(`1E+3`)},
		{"big int beyond float64", raw(`9007199254740993`)},
		{"very long number", raw(`1234567890123456789012345678901234567890`)},
		{"empty string", raw(`""`)},
		{"empty array", raw(`[]`)},
		{"empty object", raw(`{}`)},
		// key ordering
		{"object keys already sorted", raw(`{"a":1,"b":2}`)},
		{"object keys reversed", raw(`{"b":2,"a":1}`)},
		{"object keys numeric-ish", raw(`{"10":1,"9":2}`)},
		{"object keys unicode", raw(`{"` + lit(0x00e9) + `":1,"a":2}`)},
		{"nested object keys reversed", raw(`{"z":{"b":1,"a":2}}`)},
		{"array order preserved", raw(`[3,1,2]`)},
		// string escaping
		{"quote", q(`a` + `\"` + `b`)},
		{"backslash", q(`a` + `\\` + `b`)},
		{"solidus escaped in input", q(`a` + `\/` + `b`)},
		{"newline escape", q(`a` + `\n` + `b`)},
		{"tab escape", q(`a` + `\t` + `b`)},
		{"carriage return escape", q(`a` + `\r` + `b`)},
		{"backspace escape", q(`a` + `\b` + `b`)},
		{"formfeed escape", q(`a` + `\f` + `b`)},
		{"nul escape", q("a" + esc("0000") + "b")},
		{"unit separator escape", q("a" + esc("001f") + "b")},
		{"del literal", q("a" + lit(0x007f) + "b")},
		{"html chars literal", q(`a<b>c&d`)},
		{"html chars escaped in input", q("a" + esc("003c") + "b")},
		{"non ascii literal", q(lit(0x00e9))},
		{"non ascii escaped in input", q(esc("00e9"))},
		{"cjk literal", q(lit(0x7ef4) + lit(0x5ea6))},
		{"line separator literal", q("a" + lit(0x2028) + "b")},
		{"line separator escaped in input", q("a" + esc("2028") + "b")},
		{"paragraph separator literal", q("a" + lit(0x2029) + "b")},
		{"astral literal", q(lit(0x1f600))},
		{"astral escaped in input", q(esc("d83d") + esc("de00"))},
		// input-type branches of CanonicalJSONV2 itself
		{"closed string type", "plain"},
		{"closed string with quote", `a"b`},
		{"closed string non ascii", lit(0x00e9)},
		{"closed string line separator", "a" + lit(0x2028) + "b"},
		{"closed string astral", lit(0x1f600)},
		{"closed struct", DimensionFieldV2{Name: "n", Value: raw(`1`)}},
		{"closed int", 42},
		{"closed float", 1.5},
		{"closed bool", true},
		{"closed slice of string", []string{"b", "a"}},
		{"map string any", map[string]any{"b": 2, "a": 1}},
		// second round: boundaries a streaming canonicaliser has to get right
		// that the first round did not reach
		{"closed struct declaration order not alphabetical", branchProbeStruct{Zebra: 1, Apple: 2}},
		{"pointer to closed struct", &branchProbeStruct{Zebra: 1, Apple: 2}},
		{"byte slice input", []byte(`{"b":1,"a":2}`)},
		{"json marshaler type", branchProbeMarshaler{}},
		{"text marshaler type", branchProbeText{}},
		{"key needing escape", raw(`{"a` + `\"` + `b":1}`)},
		{"key with control char escape", raw(`{"a` + esc("0001") + `b":1}`)},
		{"duplicate keys differing by escape form", raw(`{"a":1,"` + esc("0061") + `":2}`)},
		{"keys differing only by escape form sorted", raw(`{"` + esc("0062") + `":1,"a":2}`)},
		{"nested empty containers", raw(`{"a":{},"b":[]}`)},
		{"deep nesting", raw(`{"a":{"b":{"c":{"d":[1,{"e":2}]}}}}`)},
		{"array of objects keys reversed", raw(`[{"b":1,"a":2},{"d":3,"c":4}]`)},
		{"object value is string with escapes", raw(`{"k":"v` + `\n` + `w"}`)},
		{"empty key", raw(`{"":1,"a":2}`)},
		{"key that is a prefix of another", raw(`{"ab":1,"a":2}`)},
		{"duplicate keys surrogate pair versus literal", raw(`{"` + esc("d83d") + esc("de00") + `":1,"` + lit(0x1f600) + `":2}`)},
		{"keys sorted across literal astral and ascii", raw(`{"` + lit(0x1f600) + `":1,"a":2}`)},
		// rejection branches
		{"empty payload", raw(``)},
		{"whitespace only", raw(`   `)},
		{"bom prefixed", raw(lit(0xfeff) + `1`)},
		{"invalid utf8", raw(`"` + string([]byte{0xff}) + `"`)},
		{"lone high surrogate", q(esc("d800"))},
		{"lone low surrogate", q(esc("dc00"))},
		{"duplicate keys", raw(`{"a":1,"a":2}`)},
		{"duplicate keys nested", raw(`{"z":{"a":1,"a":2}}`)},
		{"trailing value", raw(`1 2`)},
		{"malformed", raw(`{`)},
		{"leading zero number", raw(`01`)},
		{"leading whitespace", raw("  " + lit(9) + lit(10) + "1")},
		{"trailing whitespace", raw("1  " + lit(10))},
	}
}

func TestCanonicalBranchDiscovery(t *testing.T) {
	if os.Getenv("CANONICAL_BRANCH_DISCOVERY") == "" {
		t.Skip("set CANONICAL_BRANCH_DISCOVERY=1 to print current behaviour")
	}
	for _, p := range canonicalBranchProbes() {
		out, err := CanonicalJSONV2(p.value)
		if err != nil {
			fmt.Printf("GEN\t%s\treject\t%s"+lit(10), p.name, err.Error())
			continue
		}
		fmt.Printf("GEN\t%s\taccept\t%s"+lit(10), p.name, hex.EncodeToString(out))
	}
}

// formatCanonicalBytes shows the bytes, not a rendering of them: the whole
// question here is which byte the encoder emitted, so a terminal that folds an
// escape into the character it denotes would hide exactly what is being read.
func formatCanonicalBytes(out []byte) string {
	printable := make([]byte, 0, len(out))
	for _, b := range out {
		if b >= 0x20 && b < 0x7f {
			printable = append(printable, b)
			continue
		}
		printable = append(printable, []byte(fmt.Sprintf("<%02x>", b))...)
	}
	return string(printable)
}

// branchProbeStruct has its fields declared in an order that is not the sorted
// order, which is the one shape that separates "the encoder emitted fields in
// declaration order" from "the canonical form sorted them". Every struct in
// the first round happened to be declared alphabetically, so it could not tell
// those apart.
type branchProbeStruct struct {
	Zebra int `json:"zebra"`
	Apple int `json:"apple"`
}

type branchProbeMarshaler struct{}

func (branchProbeMarshaler) MarshalJSON() ([]byte, error) { return []byte(`{"b":1,"a":2}`), nil }

type branchProbeText struct{}

func (branchProbeText) MarshalText() ([]byte, error) { return []byte("text-value"), nil }
