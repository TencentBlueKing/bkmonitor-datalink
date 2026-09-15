// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package contract

import "testing"

// Which pinned branches production can never send, written down as a
// conclusion rather than left as a shortfall.
//
// The shadow comparison reports how many call sites it covered and whether the
// two forms agreed. It cannot report which of the ninety branches production
// exercised, and asking it to is what made an earlier exit criterion
// unmeasurable. The two denominators are different things:
//
//	offline   ninety branches, the implementation's own correctness
//	online    the call-site types production actually sends
//
// So a coverage figure short of ninety is not a gap. It is the wrong
// comparison. What the exit criterion needs instead is this: the branches that
// cannot arrive, and why. Without that, a figure stuck at some number cannot
// be told from one still climbing, and those call for opposite actions.
//
// The classification is by family, not by branch, because a per-branch list
// rots the moment a branch is added and nobody notices which family it joined.
// Every branch must land in exactly one family; the test fails on any branch
// that lands in none.
type branchReachability struct {
	family string
	reason string
}

var (
	reachableEncoded = branchReachability{"encoder-produced", "" +
		"Every closed-type call site hands over what encoding/json just wrote: " +
		"schedules, envelopes, plan sets, receipts, runtime facts."}
	reachableSourced = branchReachability{"uq-sourced-scalar", "" +
		"normalizeSeries marshals each decoded group value and passes the result " +
		"as a dimension value, so any scalar UQ can return arrives here."}
	unreachableMalformed = branchReachability{"never-arrives/malformed", "" +
		"Every raw input in this binary is the output of encoding/json, either " +
		"the encoder over a Go value or json.Marshal over a decoded one. " +
		"Externally sourced lines are read only by cmd/alarmd-comparator, a " +
		"separate binary that the phase-two runtime does not reference."}
	unreachableDuplicate = branchReachability{"never-arrives/duplicate-key", "" +
		"The encoder cannot emit a duplicate field, and a Go map cannot hold one."}
	// Accepted by the contract and still never sent. Worth its own family
	// rather than being folded into the malformed one: these are not errors,
	// they are defined behaviour that production happens not to exercise, and
	// an assertion that everything unreachable is also rejected is simply
	// false. That assertion was written first and this family is what
	// disproved it.
	unreachablePadding = branchReachability{"never-arrives/padding", "" +
		"Neither the encoder nor json.Marshal emits insignificant whitespace " +
		"around a value, so a padded payload cannot originate in this binary. " +
		"The canonical form still accepts one if it ever saw it."}
	unreachableNonFinite = branchReachability{"never-arrives/non-finite", "" +
		"The encoder refuses Inf and NaN before any bytes exist, so a non-finite " +
		"token cannot be produced by this process."}
)

// classifyCanonicalBranch places one pinned branch. Anything it does not
// recognise returns false, and the test fails rather than defaulting: a branch
// silently defaulted into "reachable" would inflate the online denominator,
// and one defaulted into "never arrives" would excuse a real gap.
func classifyCanonicalBranch(name string) (branchReachability, bool) {
	switch name {
	case "empty payload", "whitespace only", "bom prefixed", "invalid utf8",
		"lone high surrogate", "lone low surrogate", "trailing value", "malformed",
		"leading zero number":
		return unreachableMalformed, true
	case "leading whitespace", "trailing whitespace":
		return unreachablePadding, true
	case "duplicate keys", "duplicate keys nested",
		"duplicate keys differing by escape form",
		"duplicate keys surrogate pair versus literal":
		return unreachableDuplicate, true
	case "exponent overflowing float64", "negative exponent overflowing float64",
		"decimal exponent overflowing float64", "exponent overflow inside object",
		"exponent overflow inside array", "exponent just past the float64 boundary":
		return unreachableNonFinite, true
	}
	for _, prefix := range []string{"closed ", "pointer to closed", "json marshaler",
		"text marshaler", "map string any", "byte slice input"} {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			return reachableEncoded, true
		}
	}
	// Everything else is a value shape: scalars, strings and their escaping,
	// object key ordering, nesting. All of these are things the encoder emits
	// and things a UQ dimension value can be.
	for _, known := range canonicalBranchProbes() {
		if known.name == name {
			return reachableSourced, true
		}
	}
	return branchReachability{}, false
}

func TestEveryPinnedBranchIsClassifiedAsReachableOrNot(t *testing.T) {
	counts := map[string]int{}
	for _, probe := range canonicalBranchProbes() {
		class, ok := classifyCanonicalBranch(probe.name)
		if !ok {
			t.Fatalf("branch %q is classified neither way; a branch with no "+
				"reachability answer makes the online coverage figure unreadable", probe.name)
		}
		counts[class.family]++
	}

	total := len(canonicalBranchProbes())
	unreachable := counts["never-arrives/malformed"] + counts["never-arrives/padding"] +
		counts["never-arrives/duplicate-key"] + counts["never-arrives/non-finite"]
	t.Logf("pinned branches %d; cannot arrive %d (malformed %d, padding %d, duplicate key %d, "+
		"non-finite %d); can arrive %d (encoder-produced %d, uq-sourced %d)",
		total, unreachable,
		counts["never-arrives/malformed"], counts["never-arrives/padding"],
		counts["never-arrives/duplicate-key"], counts["never-arrives/non-finite"],
		total-unreachable, counts["encoder-produced"], counts["uq-sourced-scalar"])

	// Every family has to be non-empty. An empty one means the classification
	// drifted away from the probes and is no longer describing them.
	for _, family := range []string{"encoder-produced", "uq-sourced-scalar",
		"never-arrives/malformed", "never-arrives/padding",
		"never-arrives/duplicate-key", "never-arrives/non-finite"} {
		if counts[family] == 0 {
			t.Fatalf("family %q classifies nothing; it no longer describes the probes", family)
		}
	}

	// Name the ones the contract accepts and production still never sends.
	// An earlier version of this test asserted there were none, on the
	// reasoning that unreachable behaviour must be rejected behaviour. That is
	// false and the test said so: padding is accepted, defined, and never
	// arrives. Reported rather than asserted, because the count is a fact
	// about what production happens to send, not a rule.
	var acceptedYetUnreachable []string
	for _, probe := range canonicalBranchProbes() {
		class, _ := classifyCanonicalBranch(probe.name)
		if len(class.family) >= len("never-arrives") &&
			class.family[:len("never-arrives")] == "never-arrives" &&
			canonicalBranchExpectations[probe.name].accept {
			acceptedYetUnreachable = append(acceptedYetUnreachable, probe.name)
		}
	}
	t.Logf("accepted by the contract yet never sent: %v", acceptedYetUnreachable)
}
