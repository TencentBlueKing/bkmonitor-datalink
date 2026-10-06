// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The choice is resolved once, per strategy, when its Plan is built. The table
// is the whole contract: what a deployment asked for, what the strategy is, and
// what bytes come out.
func TestTheDeploymentChoiceResolvesToOneFormatPerStrategy(t *testing.T) {
	for name, test := range map[string]struct {
		configured string
		revision   int64
		want       string
		honoured   bool
	}{
		"unset with revision publishes standard raw event": {
			configured: "", revision: 7, want: contract.WireFormatStandardRawEvent, honoured: true,
		},
		"unset and no revision is the compatibility protocol": {
			configured: "", revision: 0, want: contract.WireFormatPythonCompatible, honoured: true,
		},
		"auto with revision publishes standard raw event": {
			configured: outputProtocolAuto, revision: 7, want: contract.WireFormatStandardRawEvent, honoured: true,
		},
		"auto without revision publishes compatibility protocol": {
			configured: outputProtocolAuto, revision: 0, want: contract.WireFormatPythonCompatible, honoured: true,
		},
		"legacy forces compatibility even with a revision": {
			configured: outputProtocolLegacy, revision: 7, want: contract.WireFormatPythonCompatible, honoured: true,
		},
		"native publishes the standard raw event": {
			configured: outputProtocolNative, revision: 7, want: contract.WireFormatStandardRawEvent, honoured: true,
		},
		"native cannot be honoured without a revision": {
			configured: outputProtocolNative, revision: 0, honoured: false,
		},
	} {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			format, honoured := resolveWireFormat(test.configured, test.revision)
			if honoured != test.honoured {
				t.Fatalf("honoured = %t, want %t", honoured, test.honoured)
			}
			if honoured && format != test.want {
				t.Fatalf("format = %q, want %q", format, test.want)
			}
		})
	}
}

// The one case that must never be a silent fallback: a forced native choice
// meeting a strategy with no revision has to refuse the strategy, because
// publishing it the other way is exactly what the choice was made to stop.
func TestAForcedNativeChoiceRefusesRatherThanFallsBack(t *testing.T) {
	format, honoured := resolveWireFormat(outputProtocolNative, 0)
	if honoured {
		t.Fatalf("format = %q, want the strategy refused", format)
	}
	if format == contract.WireFormatPythonCompatible {
		t.Fatal("a forced native choice must not fall back to the compatibility protocol")
	}
}

// The strategy-level read applies the readers' own rule to an object, and
// says which half decided: a frozen word is reported as frozen, and an object
// from before the choice existed gets the revision rule, named as such.
//
// Two properties, and only two. A frozen word is reported whatever the rule
// would say -- the compatibility case has a revision the rule would send the
// other way, which is what makes it a test of the word winning. An object
// with no word gets exactly what resolveWireFormat gives an unset choice,
// asserted against that function and not against a literal: what the rule
// resolves to is the rule's own contract, tested where it lives, and it has
// changed before. Pinning its current answer here would make this test red
// on every change to the rule while proving nothing about this read.
func TestTheEffectiveFormatIsTheFrozenWordOrTheRevisionRuleNamedAsSuch(t *testing.T) {
	// The word wins over the choice rule, not merely agrees with it: at least
	// one frozen case below must be a word the unset choice would not give
	// for that revision. Without one, the frozen half of the table is
	// satisfied by a read that ignores the word and applies the choice rule.
	disagreeing := 0
	for name, test := range map[string]struct {
		frozen   string
		revision int64
	}{
		"frozen standard raw event with a revision":    {frozen: contract.WireFormatStandardRawEvent, revision: 7},
		"frozen compatibility with a revision":         {frozen: contract.WireFormatPythonCompatible, revision: 7},
		"frozen trigger event with no revision":        {frozen: contract.WireFormatTriggerEvent, revision: 0},
		"historical trigger event with a revision":     {frozen: contract.WireFormatTriggerEvent, revision: 7},
		"no word and a revision goes by the rule":      {frozen: "", revision: 7},
		"no word and no revision goes by the rule too": {frozen: "", revision: 0},
	} {
		name, test := name, test
		if byChoice, _ := resolveWireFormat("", test.revision); test.frozen != "" && byChoice != test.frozen {
			disagreeing++
		}
		t.Run(name, func(t *testing.T) {
			format, decidedBy := EffectiveWireFormat(test.frozen, test.revision)
			// What the sink writes for this object: the readers' one rule,
			// asked rather than assumed.
			written := contract.ResolveOutputWireFormat(test.frozen, test.revision)
			if format != written {
				t.Fatalf("EffectiveWireFormat(%q, %d) = %q, the sink writes %q", test.frozen, test.revision, format, written)
			}
			switch {
			case test.frozen == "":
				if decidedBy != WireFormatDecidedByRevision {
					t.Fatalf("no word, decided by %q, want %s", decidedBy, WireFormatDecidedByRevision)
				}
			case written == test.frozen:
				if decidedBy != WireFormatDecidedFrozen {
					t.Fatalf("word %q written as is, decided by %q, want %s", test.frozen, decidedBy, WireFormatDecidedFrozen)
				}
			default:
				// The word and the written format differ: the read must say
				// so, not report the word as if it were written.
				if decidedBy != WireFormatDecidedHistorical {
					t.Fatalf("word %q written as %q, decided by %q, want %s", test.frozen, written, decidedBy, WireFormatDecidedHistorical)
				}
			}
		})
	}
	if disagreeing == 0 {
		t.Fatal("no frozen case disagrees with the choice rule; the table cannot tell a read of the word from a read of the rule")
	}
	// The one word the readers map elsewhere is the historical one, and the
	// table has it with a revision so the mapping is exercised.
	if format, decidedBy := EffectiveWireFormat(contract.WireFormatTriggerEvent, 7); format != contract.WireFormatStandardRawEvent || decidedBy != WireFormatDecidedHistorical {
		t.Fatalf("historical trigger event = %q by %q, want %s by %s", format, decidedBy, contract.WireFormatStandardRawEvent, WireFormatDecidedHistorical)
	}
}

// The exported list is the three words the reconciler accepts, in the same
// spelling, so a reader pinned to the list cannot drift from what the
// configuration validates.
func TestTheExportedChoicesAreTheOnesTheReconcilerAccepts(t *testing.T) {
	reconciler := &SourceReconciler{}
	for _, word := range OutputProtocolChoices {
		if err := reconciler.ConfigureOutputProtocol(word); err != nil {
			t.Fatalf("ConfigureOutputProtocol(%q) = %v, want accepted", word, err)
		}
	}
	if len(OutputProtocolChoices) != 3 {
		t.Fatalf("choices = %v, want the three the configuration validates", OutputProtocolChoices)
	}
	if err := reconciler.ConfigureOutputProtocol("shadow"); err == nil {
		t.Fatal("a word outside the list was accepted")
	}
}
