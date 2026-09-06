package controlplane

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A cache admission bound only: larger valid activations still load normally.
// It bounds retained wire bytes, not the heap of the decoded state and index.
const parsedActivationMaxPayloadBytes = 2 << 20

type parsedActivation struct {
	payload string
	state   ActivationState
	byPlan  map[execution.PlanIdentity]execution.PlanActivationFact
}

// The single entry is immutable after publication and never leaves this package.
type parsedActivationCache struct {
	mu    sync.Mutex
	entry *parsedActivation
}

func (cache *parsedActivationCache) clear() {
	cache.mu.Lock()
	cache.entry = nil
	cache.mu.Unlock()
}

func (cache *parsedActivationCache) load(payload string) (*parsedActivation, error) {
	if len(payload) > parsedActivationMaxPayloadBytes {
		cache.clear()
		return parseActivation(payload)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entry != nil && cache.entry.payload == payload {
		return cache.entry, nil
	}
	cache.entry = nil
	entry, err := parseActivation(payload)
	if err != nil {
		return nil, err
	}
	entry.payload = payload
	cache.entry = entry
	return entry, nil
}

func parseActivation(payload string) (*parsedActivation, error) {
	var state ActivationState
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		return nil, &PersistedActivationCorruptError{Err: fmt.Errorf("decode: %w", err)}
	}
	if err := validateActivationState(state); err != nil {
		return nil, &PersistedActivationCorruptError{Err: err}
	}
	entry := &parsedActivation{state: state, byPlan: make(map[execution.PlanIdentity]execution.PlanActivationFact, len(state.Plans))}
	for _, record := range state.Plans {
		entry.byPlan[record.Fact.Plan] = record.Fact
	}
	return entry, nil
}

func (entry *parsedActivation) cloneState() ActivationState {
	state := entry.state
	if state.Pending != nil {
		pending := *state.Pending
		state.Pending = &pending
	}
	state.Plans = slices.Clone(state.Plans)
	state.Draining = slices.Clone(state.Draining)
	return state
}
