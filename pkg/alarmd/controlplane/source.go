package controlplane

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

var ErrObservationUnstable = errors.New("alarmd controlplane: source observation unstable")

// ErrActiveSetNotCanonical marks an active set refused because it lists an
// identity twice or lists an empty one. Like the invalid identity, one
// element refuses the whole set, and every round the same way.
var ErrActiveSetNotCanonical = errors.New("alarmd controlplane: invalid active strategy set")

type StrategySource interface {
	ActiveStrategyIDs(context.Context) ([]string, error)
	Strategies(context.Context, []string) ([]SourceStrategy, error)
}

// SourceChangeSignal is the marker a source's publisher leaves after every
// change it makes to the source, and only then. Value is compared verbatim
// from one round to the next. WrittenAt is what the marker says about when
// the publisher wrote it, for reporting its age, and is zero when it says
// nothing. Present false means the source had no marker to read this round.
//
// HoldsLastGoodFor is the publisher's own statement, made for this very
// signal, that a strategy leaves the active set only for a fact about the
// strategy itself - disabled, deleted, nothing left of it to run - and never
// because publishing it failed: one that fails to publish keeps its last good
// document. With it, a strategy missing from the set is gone for its own
// reasons, and leaves the Catalog at once. Without it - a publisher that says
// nothing - a missing strategy may be one the publisher dropped by mistake,
// and it serves the removal grace (AbsenceGracePeriod). The statement is
// about one active set, and names it: HoldsLastGoodFor is the SHA-256, in
// lowercase hex, of the exact bytes the publisher stored as that set, and
// empty when there is no statement. It holds for an observation only when
// the active set that observation read is those very bytes
// (ActiveSetDigestSource); a set rewritten since, by anyone, is a set the
// statement does not cover.
type SourceChangeSignal struct {
	Present          bool
	Value            string
	WrittenAt        time.Time
	HoldsLastGoodFor string
}

// ChangeSignalSource is a StrategySource whose publisher leaves a
// SourceChangeSignal. A reconciler uses it to decide whether a round has to
// read the strategy documents at all; the signal never enters an observation
// identity.
type ChangeSignalSource interface {
	ChangeSignal(context.Context) (SourceChangeSignal, error)
}

// ActiveSetDigestSource is a StrategySource that can name the exact bytes its
// publisher stored as the active set. ActiveStrategyIDsWithDigest returns
// what ActiveStrategyIDs returns and, from the same read, the SHA-256 of the
// stored value exactly as the store returned it, in lowercase hex. A source
// that cannot name them reads as one whose publisher made no statement about
// the set (SourceChangeSignal.HoldsLastGoodFor).
type ActiveSetDigestSource interface {
	ActiveStrategyIDsWithDigest(context.Context) ([]string, string, error)
}

type StableObservation struct {
	ObservationID string
	Strategies    []SourceStrategy
}

func ObserveStable(ctx context.Context, source StrategySource) (StableObservation, error) {
	if source == nil {
		return StableObservation{}, errors.New("alarmd controlplane: incomplete source observation request")
	}
	first, err := observeCycle(ctx, source)
	if err != nil {
		return StableObservation{}, err
	}
	second, err := observeCycle(ctx, source)
	if err != nil {
		return StableObservation{}, err
	}
	if !equalStrings(first.ids, second.ids) || !equalStrings(first.digests, second.digests) {
		return StableObservation{}, ErrObservationUnstable
	}
	observationID, err := deriveObservationID(second.strategies)
	if err != nil {
		return StableObservation{}, err
	}
	return StableObservation{ObservationID: observationID, Strategies: second.strategies}, nil
}

func deriveObservationID(strategies []SourceStrategy) (string, error) {
	type item struct{ ID, Digest string }
	items := make([]item, 0, len(strategies))
	for _, strategy := range strategies {
		if !validObservedStrategy(strategy) {
			return "", ErrObservationUnstable
		}
		digest, err := strategyDigest(strategy)
		if err != nil {
			return "", err
		}
		items = append(items, item{strategy.SourceID, digest})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return contract.DeriveCanonicalDigestV2("alarmd-source-observation-v1", items)
}

// observedCycle is one read of the source. activeSet is the digest of the
// stored active set exactly as the read that built the cycle returned it -
// the first of its two reads, whose ids the documents were asked for - and
// empty when the source cannot name those bytes (ActiveSetDigestSource).
type observedCycle struct {
	ids, digests []string
	strategies   []SourceStrategy
	activeSet    string
}

func observeCycle(ctx context.Context, source StrategySource) (observedCycle, error) {
	before, activeSet, err := readNamedActiveSet(ctx, source)
	if err != nil {
		return observedCycle{}, err
	}
	strategies, err := source.Strategies(ctx, before)
	if err != nil {
		return observedCycle{}, exitAt(SourceRefreshExitDocuments, err)
	}
	after, err := readActiveSet(ctx, source)
	if err != nil {
		return observedCycle{}, err
	}
	if !equalStrings(before, after) || len(strategies) != len(before) {
		return observedCycle{}, exitAt(SourceRefreshExitObservationUnstable, ErrObservationUnstable)
	}
	byID := make(map[string]SourceStrategy, len(strategies))
	for _, strategy := range strategies {
		if !validObservedStrategy(strategy) {
			return observedCycle{}, exitAt(SourceRefreshExitObservationUnstable, ErrObservationUnstable)
		}
		if _, duplicate := byID[strategy.SourceID]; duplicate {
			return observedCycle{}, exitAt(SourceRefreshExitObservationUnstable, ErrObservationUnstable)
		}
		byID[strategy.SourceID] = strategy
	}
	ordered := make([]SourceStrategy, 0, len(before))
	digests := make([]string, 0, len(before))
	for _, id := range before {
		strategy, ok := byID[id]
		if !ok {
			return observedCycle{}, exitAt(SourceRefreshExitObservationUnstable, ErrObservationUnstable)
		}
		digest, err := sourceFactsDigest(strategy)
		if err != nil {
			return observedCycle{}, exitAt(SourceRefreshExitObservationUnstable, ErrObservationUnstable)
		}
		strategy.digest = digest
		ordered = append(ordered, strategy)
		digests = append(digests, digest)
	}
	return observedCycle{ids: before, digests: digests, strategies: ordered, activeSet: activeSet}, nil
}

// readActiveSet reads the active set and makes it canonical, claiming the
// exit for whichever of the two refused it.
func readActiveSet(ctx context.Context, source StrategySource) ([]string, error) {
	return canonicalActiveSetRead(source.ActiveStrategyIDs(ctx))
}

// readNamedActiveSet is readActiveSet that also returns the digest of the
// bytes the read returned, when the source can name them, and empty
// otherwise. Only the read a cycle is built from needs it.
func readNamedActiveSet(ctx context.Context, source StrategySource) ([]string, string, error) {
	named, ok := source.(ActiveSetDigestSource)
	if !ok {
		ids, err := readActiveSet(ctx, source)
		return ids, "", err
	}
	ids, digest, err := named.ActiveStrategyIDsWithDigest(ctx)
	ids, err = canonicalActiveSetRead(ids, err)
	if err != nil {
		return nil, "", err
	}
	return ids, digest, nil
}

func canonicalActiveSetRead(ids []string, err error) ([]string, error) {
	if err != nil {
		return nil, exitAt(activeSetExit(err), err)
	}
	ids, err = canonicalActiveSet(ids)
	if err != nil {
		return nil, exitAt(SourceRefreshExitActiveSetDuplicate, err)
	}
	return ids, nil
}

func validObservedStrategy(strategy SourceStrategy) bool {
	if strategy.SourceID == "" {
		return false
	}
	if len(strategy.Document) > 0 {
		return true
	}
	return strategy.SourceDisposition != nil && strategy.SourceDisposition.SourceID == strategy.SourceID &&
		strategy.SourceDisposition.Scope == "STRATEGY" && strategy.SourceDisposition.Reason != "" &&
		strategy.SourceDisposition.Disposition == DispositionSourceIncomplete
}

// strategyDigest returns the source facts digest, computing it only when the
// observation has not already.
func strategyDigest(strategy SourceStrategy) (string, error) {
	if strategy.digest != "" {
		return strategy.digest, nil
	}
	return sourceFactsDigest(strategy)
}

func sourceFactsDigest(strategy SourceStrategy) (string, error) {
	return contract.DeriveCanonicalDigestV2("alarmd-source-object-v2", struct {
		Document    []byte             `json:"document"`
		Identity    SourceIdentity     `json:"identity"`
		Disposition *ObjectDisposition `json:"disposition,omitempty"`
	}{Document: strategy.Document, Identity: strategy.Identity, Disposition: strategy.SourceDisposition})
}

func canonicalActiveSet(ids []string) ([]string, error) {
	result := append([]string(nil), ids...)
	sort.Strings(result)
	for index, id := range result {
		if id == "" || (index > 0 && result[index-1] == id) {
			return nil, fmt.Errorf("%w: element %q at %d of %d", ErrActiveSetNotCanonical, id, index, len(result))
		}
	}
	return result, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
