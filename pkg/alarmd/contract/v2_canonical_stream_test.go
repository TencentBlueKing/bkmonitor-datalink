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

// withCanonicalStream runs body with the single-pass form on and restores the
// previous setting afterwards.
func withCanonicalStream(t *testing.T, body func()) {
	t.Helper()
	previous := SetCanonicalStreamEnabled(true)
	defer SetCanonicalStreamEnabled(previous)
	body()
}

// The pinned table is the denominator, so the replacement is checked against
// exactly the same ninety branches, with the same expectations, generated from
// the implementation being replaced before this one existed. Anything the
// single-pass form declines falls through and is answered by the established
// path, so this test alone cannot tell "reproduced it" from "declined it"; the
// counters are what separate those, and the next test reads them.
func TestCanonicalStreamMatchesThePinnedBranchTable(t *testing.T) {
	withCanonicalStream(t, func() {
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
	})
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
	withCanonicalStream(t, func() {
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
	})
}

// The rejection branches must be declined, not answered. A single-pass form
// that produced bytes for input the established path refuses would be a
// widening of what the contract accepts, which is the one direction that is
// silent in production: the query succeeds and nobody reports it.
func TestCanonicalStreamDeclinesEveryRejectedBranch(t *testing.T) {
	withCanonicalStream(t, func() {
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
	})
}

// canonicalStreamAgrees runs one value through both settings and requires the
// same answer. Comparing the two paths inside one process on one input is the
// only comparison that stays valid for fuzz input, where no table can.
func canonicalStreamAgrees(t *testing.T, value any) {
	t.Helper()
	previous := SetCanonicalStreamEnabled(false)
	defer SetCanonicalStreamEnabled(previous)
	establishedOut, establishedErr := CanonicalJSONV2(value)
	SetCanonicalStreamEnabled(true)
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
		name    string
		enabled bool
	}{{"established", false}, {"stream", true}} {
		b.Run(arm.name, func(b *testing.B) {
			previous := SetCanonicalStreamEnabled(arm.enabled)
			defer SetCanonicalStreamEnabled(previous)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := CanonicalJSONV2(input); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Shadow must not change any answer, and must actually be comparing. A shadow
// that silently compared nothing would report a clean sheet, which is the same
// failure the declined counter exists to prevent one level down.
func TestCanonicalShadowComparesWithoutChangingAnswers(t *testing.T) {
	previousShadow := SetCanonicalStreamShadow(true)
	defer SetCanonicalStreamShadow(previousShadow)
	previousStream := SetCanonicalStreamEnabled(false)
	defer SetCanonicalStreamEnabled(previousStream)

	before := ReadCanonicalShadowCounts()
	for _, probe := range canonicalBranchProbes() {
		want := canonicalBranchExpectations[probe.name]
		out, err := CanonicalJSONV2(probe.value)
		if want.accept {
			if err != nil {
				t.Fatalf("%s: shadow changed an answer: %v", probe.name, err)
			}
			if got := hex.EncodeToString(out); got != want.wantHex {
				t.Fatalf("%s: shadow changed the bytes:\n want %s\n got  %s", probe.name, want.wantHex, got)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: shadow turned a rejection into an accept", probe.name)
		}
	}
	after := ReadCanonicalShadowCounts()
	if after.Compared == before.Compared {
		t.Fatal("shadow compared nothing; a clean sheet here would mean nothing")
	}
	if after.Agreed == before.Agreed {
		t.Fatal("shadow agreed on nothing; it is declining everything")
	}
	if after.BytesDiffer != before.BytesDiffer ||
		after.VerdictDiffer != before.VerdictDiffer ||
		after.PanicDiffer != before.PanicDiffer {
		t.Fatalf("shadow found divergence on the pinned branches: %+v", after)
	}
}

// The detector itself has to be shown to fire, or "zero divergence" is a
// statement about the detector rather than about the two implementations. This
// hands it an answer that is deliberately wrong instead of breaking the
// implementation to provoke one.
func TestCanonicalShadowDetectsADivergenceItIsGiven(t *testing.T) {
	raw := []byte(`{"b":1,"a":2}`)
	correct, err := CanonicalJSONV2(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	wrong := append([]byte(nil), correct...)
	wrong[len(wrong)-2] = '9' // change the last value

	before := ReadCanonicalShadowCounts()
	compareCanonicalShadow("test.Injected", raw, wrong, nil)
	after := ReadCanonicalShadowCounts()
	if after.BytesDiffer != before.BytesDiffer+1 {
		t.Fatalf("detector did not fire on a planted difference: %+v", after)
	}

	var found bool
	for _, sample := range ReadCanonicalShadowSamples() {
		if sample.GoType == "test.Injected" {
			found = true
			if sample.Class != "bytes_differ" {
				t.Fatalf("wrong class %q", sample.Class)
			}
			if sample.Chain != "o" {
				t.Fatalf("want the offset reported inside one object, got chain %q", sample.Chain)
			}
			if sample.Old == sample.New {
				t.Fatalf("both sides reported the same byte %q", sample.Old)
			}
		}
	}
	if !found {
		t.Fatal("no fingerprint recorded for the planted difference")
	}
}
