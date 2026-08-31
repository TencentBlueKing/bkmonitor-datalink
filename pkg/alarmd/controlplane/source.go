package controlplane

import (
	"context"
	"errors"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

var ErrObservationUnstable = errors.New("alarmd controlplane: source observation unstable")

type StrategySource interface {
	ActiveStrategyIDs(context.Context) ([]string, error)
	Strategies(context.Context, []string) ([]SourceStrategy, error)
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
		if strategy.SourceID == "" || len(strategy.Document) == 0 {
			return "", ErrObservationUnstable
		}
		digest, err := sourceFactsDigest(strategy)
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
	before, err := source.ActiveStrategyIDs(ctx)
	if err != nil {
		return observedCycle{}, err
	}
	before, err = canonicalActiveSet(before)
	if err != nil {
		return observedCycle{}, err
	}
	strategies, err := source.Strategies(ctx, before)
	if err != nil {
		return observedCycle{}, err
	}
	after, err := source.ActiveStrategyIDs(ctx)
	if err != nil {
		return observedCycle{}, err
	}
	after, err = canonicalActiveSet(after)
	if err != nil {
		return observedCycle{}, err
	}
	if !equalStrings(before, after) || len(strategies) != len(before) {
		return observedCycle{}, ErrObservationUnstable
	}
	byID := make(map[string]SourceStrategy, len(strategies))
	for _, strategy := range strategies {
		if strategy.SourceID == "" || len(strategy.Document) == 0 {
			return observedCycle{}, ErrObservationUnstable
		}
		if _, duplicate := byID[strategy.SourceID]; duplicate {
			return observedCycle{}, ErrObservationUnstable
		}
		byID[strategy.SourceID] = strategy
	}
	ordered := make([]SourceStrategy, 0, len(before))
	digests := make([]string, 0, len(before))
	for _, id := range before {
		strategy, ok := byID[id]
		if !ok {
			return observedCycle{}, ErrObservationUnstable
		}
		digest, err := sourceFactsDigest(strategy)
		if err != nil {
			return observedCycle{}, ErrObservationUnstable
		}
		ordered = append(ordered, strategy)
		digests = append(digests, digest)
	}
	return observedCycle{ids: before, digests: digests, strategies: ordered}, nil
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
			return nil, errors.New("alarmd controlplane: invalid active strategy set")
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
