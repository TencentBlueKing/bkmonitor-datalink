// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"

// The deployment's choice of protocol. These three words are the configuration
// surface; they are spelled here rather than imported from the configuration
// package so the control plane does not depend on it, and the configuration
// validates the same three.
const (
	outputProtocolAuto   = "auto"
	outputProtocolLegacy = "legacy"
	outputProtocolNative = "native"
)

// OutputProtocolChoices is the same three words as a list, for the readers
// that show a replica's choice and need to know every word it can be. It is
// the vocabulary the fleet publishes on a snapshot; a word outside it is a
// build this reader does not know, not a fourth protocol.
var OutputProtocolChoices = []string{outputProtocolAuto, outputProtocolLegacy, outputProtocolNative}

// How a Plan's effective wire format was decided, as the strategy-level read
// reports it. Closed: a reader shows these words and no others.
const (
	// WireFormatDecidedFrozen: the output context carries the word the
	// control leader froze when it built the Plan, and that word is what the
	// sink writes.
	WireFormatDecidedFrozen = "FROZEN"
	// WireFormatDecidedByRevision: the output context predates the choice and
	// carries no word; the rule the readers apply to an object without one
	// decides, from the frozen revision.
	WireFormatDecidedByRevision = "REVISION_RULE"
	// WireFormatDecidedHistorical: the output context carries a word from an
	// earlier rule -- trigger_event_v1, which no new Plan selects -- and the
	// readers resolve it to a current external format without rewriting the
	// object. The frozen word and the written format then differ, and the
	// read reports both.
	WireFormatDecidedHistorical = "HISTORICAL_WORD_RESOLVED"
)

// WireFormatDecisions is every word DecidedBy can carry.
var WireFormatDecisions = []string{WireFormatDecidedFrozen, WireFormatDecidedByRevision, WireFormatDecidedHistorical}

// EffectiveWireFormat is the format the sink writes for a Plan whose output
// context says frozen, and how that was decided. It is
// contract.ResolveOutputWireFormat -- the one rule the evaluator's recovery
// gate and the sink apply to a frozen word -- with the decision named, so
// the read that explains "what did auto choose for this strategy" cannot say
// one thing while the sink writes another. A frozen word that the rule leaves
// alone is FROZEN; no word is the revision rule; a word the rule maps to a
// different format is historical, and the caller shows the word beside the
// format rather than either alone.
//
// It does not re-derive the format from the deployment's current choice:
// that is a different fact, and can have changed since the Plan was built.
func EffectiveWireFormat(frozen string, snapshotRevision int64) (format, decidedBy string) {
	format = contract.ResolveOutputWireFormat(frozen, snapshotRevision)
	switch {
	case frozen == "":
		return format, WireFormatDecidedByRevision
	case format == frozen:
		return format, WireFormatDecidedFrozen
	default:
		return format, WireFormatDecidedHistorical
	}
}

// resolveWireFormat turns the deployment's choice into the format this Plan
// publishes, which is a different vocabulary on purpose: the choice says what a
// deployment wants, the format says what bytes are written, and "auto" is not a
// format.
//
// Auto selects one of the two external formats. A frozen revision makes a
// strategy publishable as a standard raw event.
// Resolving it into the Plan means the Plan says which format it uses instead
// of leaving every reader to re-derive it from the revision.
//
// The second result is false when the choice cannot be honoured for this
// strategy, which happens only for a forced native choice without a revision.
func resolveWireFormat(configured string, snapshotRevision int64) (string, bool) {
	switch configured {
	case outputProtocolLegacy:
		return contract.WireFormatPythonCompatible, true
	case outputProtocolNative:
		if snapshotRevision == 0 {
			return "", false
		}
		return contract.WireFormatStandardRawEvent, true
	}
	if snapshotRevision == 0 {
		return contract.WireFormatPythonCompatible, true
	}
	return contract.WireFormatStandardRawEvent, true
}
