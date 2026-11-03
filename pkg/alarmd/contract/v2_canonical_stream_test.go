// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package contract

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

// canonicalEstablishedOf is CanonicalJSONV2 answered by the established form
// alone, on the same prepared input: the path a declined input takes, held
// against what the single-pass form answers.
func canonicalEstablishedOf(value any) ([]byte, error) {
	raw, closed, final, err := canonicalInputV2(value)
	if err != nil || final != nil {
		return final, err
	}
	return canonicalEstablishedV2(raw, closed)
}

// The pinned table is the denominator, so the replacement is checked against
// exactly the same ninety branches, with the same expectations, generated from
// the implementation being replaced before this one existed. Anything the
// single-pass form declines falls through and is answered by the established
// path, so this test alone cannot tell "reproduced it" from "declined it"; the
// counters are what separate those, and the next test reads them.
func TestCanonicalStreamMatchesThePinnedBranchTable(t *testing.T) {
	for _, probe := range canonicalBranchProbes() {
		want, ok := canonicalBranchExpectations[probe.name]
		if !ok {
			t.Fatalf("branch %q has no expectation", probe.name)
		}
		t.Run(probe.name, func(t *testing.T) {
			out, err := CanonicalJSONV2(probe.value)
			if want.accept {
				if err != nil {
					t.Fatalf("want accept, got error %v", err)
				}
				if got := hex.EncodeToString(out); got != want.wantHex {
					t.Fatalf("canonical bytes moved:\n want %s\n got  %s", want.wantHex, got)
				}
				return
			}
			if err == nil {
				t.Fatalf("want rejection, got accept %s", hex.EncodeToString(out))
			}
			wantError, decodeErr := hex.DecodeString(want.wantErrorHex)
			if decodeErr != nil {
				t.Fatalf("expectation is not hex: %v", decodeErr)
			}
			if err.Error() != string(wantError) {
				t.Fatalf("rejection text moved:\n want %q\n got  %q", wantError, err.Error())
			}
		})
	}
}

// Every accepted branch that reaches the decode-and-re-encode path must be
// answered by the single-pass form, not declined into it. Without this the
// table above would still pass with the new code doing nothing at all.
//
// The two branches that never reach it are named rather than counted: a closed
// string of valid UTF-8 returns earlier, before either path is consulted.
func TestCanonicalStreamAnswersEveryAcceptedBranchItShould(t *testing.T) {
	skipsBothPaths := map[string]bool{
		"closed string type":           true,
		"closed string with quote":     true,
		"closed string non ascii":      true,
		"closed string line separator": true,
		"closed string astral":         true,
	}
	for _, probe := range canonicalBranchProbes() {
		want := canonicalBranchExpectations[probe.name]
		if !want.accept || skipsBothPaths[probe.name] {
			continue
		}
		t.Run(probe.name, func(t *testing.T) {
			beforeServed, beforeDeclined := CanonicalStreamCounts()
			if _, err := CanonicalJSONV2(probe.value); err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			served, declined := CanonicalStreamCounts()
			if declined != beforeDeclined {
				t.Fatalf("declined into the established path; the saving is not being taken")
			}
			if served == beforeServed {
				t.Fatalf("neither served nor declined; the switch was not consulted")
			}
		})
	}
}

// The rejection branches must be declined, not answered. A single-pass form
// that produced bytes for input the established path refuses would be a
// widening of what the contract accepts, which is the one direction that is
// silent in production: the query succeeds and nobody reports it.
func TestCanonicalStreamDeclinesEveryRejectedBranch(t *testing.T) {
	for _, probe := range canonicalBranchProbes() {
		want := canonicalBranchExpectations[probe.name]
		if want.accept {
			continue
		}
		t.Run(probe.name, func(t *testing.T) {
			beforeServed, _ := CanonicalStreamCounts()
			if _, err := CanonicalJSONV2(probe.value); err == nil {
				t.Fatal("want rejection, got accept")
			}
			if served, _ := CanonicalStreamCounts(); served != beforeServed {
				t.Fatal("the single-pass form answered an input the contract rejects")
			}
		})
	}
}

// canonicalStreamAgrees runs one value through both forms and requires the
// same answer. Comparing the two paths inside one process on one input is the
// only comparison that stays valid for fuzz input, where no table can.
func canonicalStreamAgrees(t *testing.T, value any) {
	t.Helper()
	establishedOut, establishedErr := canonicalEstablishedOf(value)
	streamOut, streamErr := CanonicalJSONV2(value)
	if (establishedErr == nil) != (streamErr == nil) {
		t.Fatalf("value=%v: established err=%v, stream err=%v; the rejection boundary moved",
			value, establishedErr, streamErr)
	}
	if establishedErr != nil {
		if establishedErr.Error() != streamErr.Error() {
			t.Fatalf("value=%v: rejection text moved:\n established %q\n stream      %q",
				value, establishedErr.Error(), streamErr.Error())
		}
		return
	}
	if string(establishedOut) != string(streamOut) {
		t.Fatalf("value=%v: canonical bytes moved:\n established %s\n stream      %s",
			value, hex.EncodeToString(establishedOut), hex.EncodeToString(streamOut))
	}
}

func TestCanonicalStreamAgreesOnTheCorpus(t *testing.T) {
	for _, entry := range dimensionIdentityCorpus {
		t.Run(entry.name, func(t *testing.T) {
			canonicalStreamAgrees(t, json.RawMessage(entry.value))
		})
	}
	for _, probe := range canonicalBranchProbes() {
		t.Run("branch/"+probe.name, func(t *testing.T) {
			canonicalStreamAgrees(t, probe.value)
		})
	}
}

func FuzzCanonicalStreamAgreesWithTheEstablishedPath(f *testing.F) {
	for _, entry := range dimensionIdentityCorpus {
		f.Add(entry.value)
	}
	for _, probe := range canonicalBranchProbes() {
		if raw, ok := probe.value.(json.RawMessage); ok {
			f.Add(string(raw))
		}
	}
	f.Fuzz(func(t *testing.T, payload string) {
		canonicalStreamAgrees(t, json.RawMessage(payload))
	})
}

// Both settings in one binary, alternating, because the two numbers are only
// comparable if they met the same machine.
func BenchmarkCanonicalStreamVersusEstablished(b *testing.B) {
	input := json.RawMessage(`{"dimensions":{"bk_target_ip":"10.0.0.1","bk_cloud_id":"0",` +
		`"instance":"host-000123","job":"node-exporter"},"value":12345.678,"time":1757740800}`)
	for _, arm := range []struct {
		name   string
		encode func(any) ([]byte, error)
	}{{"established", canonicalEstablishedOf}, {"stream", CanonicalJSONV2}} {
		b.Run(arm.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := arm.encode(input); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The level arenas are reused across objects and, through the pool, across
// calls. If one stops being truncated, every answer stays correct -- the
// members hold absolute offsets, so the spans still point at the right bytes
// -- and the buffer grows without bound for the life of the process.
//
// That is why this test exists at all. Removing the truncation was tried as a
// mutation and the whole suite stayed green, correctly: nothing about the
// output is wrong. A leak that produces right answers cannot be caught by
// checking answers, so it has to be checked where it happens.
func TestCanonicalStreamLevelArenasAreTruncatedBetweenObjects(t *testing.T) {
	stream := &canonicalStream{}
	run := func(payload string) {
		t.Helper()
		stream.src, stream.pos = []byte(payload), 0
		if _, ok := stream.value(nil, 0); !ok {
			t.Fatalf("declined %s", payload)
		}
	}
	// Two objects with keys of the same total length. If the arena were not
	// truncated the second call would leave twice the bytes behind.
	run(`{"alpha":1,"bravo":2}`)
	after := len(stream.levels[0].keys)
	run(`{"charl":1,"delta":2}`)
	if grown := len(stream.levels[0].keys); grown != after {
		t.Fatalf("key arena carried %d bytes into the next object, expected %d; "+
			"answers stay correct and the buffer grows for the life of the process",
			grown, after)
	}

	// Siblings inside one call use the same level and must not accumulate
	// either. Three objects at depth 1, each with one short key.
	stream.src, stream.pos = []byte(`{"a":{"k":1},"b":{"k":2},"c":{"k":3}}`), 0
	if _, ok := stream.value(nil, 0); !ok {
		t.Fatal("declined the nested payload")
	}
	if held := len(stream.levels[1].keys); held != 1 {
		t.Fatalf("depth-1 arena holds %d key bytes after three one-key siblings, expected 1", held)
	}
}
