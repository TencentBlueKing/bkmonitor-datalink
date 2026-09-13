package controlplane

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

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
	// applied is the record revision of the Activation last parsed through
	// this cache, kept apart from entry because an Activation too large to
	// cache is still the one this process executes by.
	applied atomic.Uint64
}

func (cache *parsedActivationCache) clear() {
	cache.mu.Lock()
	cache.entry = nil
	cache.mu.Unlock()
}

func (cache *parsedActivationCache) load(payload string) (*parsedActivation, error) {
	if len(payload) > parsedActivationMaxPayloadBytes {
		cache.clear()
		entry, err := parseActivation(payload)
		if err != nil {
			return nil, err
		}
		cache.applied.Store(entry.state.RecordRevision)
		return entry, nil
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
	cache.applied.Store(entry.state.RecordRevision)
	return entry, nil
}

// AppliedActivationRevision is the record revision of the Activation this
// process last parsed: the one its Slot reads execute by and its fleet page
// reports. Zero before the first parse. A worker writes it into its heartbeat
// as the acknowledgement the control plane can compare with what it published.
func (repository *RedisCatalogRepository) AppliedActivationRevision() uint64 {
	if repository == nil {
		return 0
	}
	return repository.activationCache.applied.Load()
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
