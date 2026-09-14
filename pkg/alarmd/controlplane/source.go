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
type SourceChangeSignal struct {
	Present   bool
	Value     string
	WrittenAt time.Time
}

// ChangeSignalSource is a StrategySource whose publisher leaves a
// SourceChangeSignal. A reconciler uses it to decide whether a round has to
// read the strategy documents at all; the signal never enters an observation.
type ChangeSignalSource interface {
	ChangeSignal(context.Context) (SourceChangeSignal, error)
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

type observedCycle struct {
	ids, digests []string
	strategies   []SourceStrategy
}

func observeCycle(ctx context.Context, source StrategySource) (observedCycle, error) {
	before, err := readActiveSet(ctx, source)
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
	return observedCycle{ids: before, digests: digests, strategies: ordered}, nil
}

// readActiveSet reads the active set and makes it canonical, claiming the
// exit for whichever of the two refused it.
func readActiveSet(ctx context.Context, source StrategySource) ([]string, error) {
	ids, err := source.ActiveStrategyIDs(ctx)
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
