// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type Backend interface {
	// MGet returns one value per key in the same order. A nil value means
	// missing; an existing empty value must be returned as a non-nil slice and
	// will be classified as corrupt state.
	MGet(context.Context, []string) ([][]byte, error)
	// SetMany executes one bounded pipeline batch. It may have partially
	// succeeded when it returns an error; replaying the complete batch is safe.
	SetMany(context.Context, []BackendWrite) error
}

type BackendWrite struct {
	Key   string
	Value []byte
	TTL   time.Duration
}

type StorageTarget struct {
	Name    string
	Backend Backend
}

// StorageRouter chooses storage only from tenant and strategy identity. It is
// unrelated to Kafka partitions, dimensions, or process ownership.
type StorageRouter interface {
	Route(tenantID, strategyID string) (StorageTarget, error)
}

type StoreLimits struct {
	MaxKeysPerBatch     int
	MaxKeyBytesPerBatch int
	MaxLoadedBytes      int
	MaxWrittenBytes     int
}

type StoreOptions struct {
	Prefix        string
	Codec         *Codec
	Router        StorageRouter
	Limits        StoreLimits
	MinTTL        time.Duration
	MaxTTL        time.Duration
	RestartMargin time.Duration
	Observer      Observer
}

type Store struct {
	options StoreOptions
}

type LoadWindowSpec struct {
	Identity     RuntimeIdentity
	Requirements []LevelRequirement
}

type LoadWindowsRequest struct {
	Items []LoadWindowSpec
}

type LoadStatus string

const (
	LoadFound        LoadStatus = "FOUND"
	LoadMissing      LoadStatus = "MISSING"
	LoadResetCorrupt LoadStatus = "RESET_CORRUPT"
	LoadUnavailable  LoadStatus = "UNAVAILABLE"
)

type LoadedWindow struct {
	Identity     RuntimeIdentity
	Key          string
	Requirements []LevelRequirement
	Window       *Window
	Status       LoadStatus
	Err          error
}

type LoadWindowsResult struct {
	Items []LoadedWindow
}

type WriteWindowsRequest struct {
	Items []LoadedWindow
}

type WriteStatus string

const (
	WriteNoop               WriteStatus = "NOOP"
	WritePersisted          WriteStatus = "PERSISTED"
	WriteUnavailable        WriteStatus = "UNAVAILABLE"
	WriteInvariantViolation WriteStatus = "INVARIANT_VIOLATION"
)

type WriteWindowResult struct {
	Key          string
	Status       WriteStatus
	EncodedBytes int
	TTL          time.Duration
	Err          error
}

type WriteWindowsResult struct {
	Items []WriteWindowResult
}

// RuntimeStateStore returns deterministic item failures in the result, while
// shared router/backend failures use error. A caller must inspect every write
// item: only NOOP and PERSISTED are complete. UNAVAILABLE and
// INVARIANT_VIOLATION must not advance the input completion watermark or Kafka
// offset. M3/M7 must reject deterministic levels/points/estimated-size budget
// violations before event output; a post-admission state budget error is an
// internal invariant violation, not a committable local terminal.
type RuntimeStateStore interface {
	LoadWindows(context.Context, LoadWindowsRequest) (LoadWindowsResult, error)
	WriteWindows(context.Context, WriteWindowsRequest) (WriteWindowsResult, error)
}

type routedLoad struct {
	index  int
	key    string
	target StorageTarget
	spec   LoadWindowSpec
}

type routedWrite struct {
	index  int
	target StorageTarget
	write  BackendWrite
	window *Window
}

func NewStore(options StoreOptions) (*Store, error) {
	if options.Prefix == "" || options.Codec == nil || options.Router == nil {
		return nil, fmt.Errorf("state: prefix, codec, and router are required")
	}
	if options.Limits.MaxKeysPerBatch <= 0 || options.Limits.MaxKeyBytesPerBatch <= 0 ||
		options.Limits.MaxLoadedBytes <= 0 || options.Limits.MaxWrittenBytes <= 0 {
		return nil, fmt.Errorf("state: store limits must be positive")
	}
	if options.Limits.MaxLoadedBytes < options.Codec.limits.MaxEncodedBytes {
		return nil, fmt.Errorf("%w: loaded bytes cannot admit one maximum state", ErrStateBudget)
	}
	if options.MinTTL <= 0 || options.MaxTTL < options.MinTTL || options.RestartMargin < 0 {
		return nil, fmt.Errorf("state: invalid TTL limits")
	}
	return &Store{options: options}, nil
}

func (store *Store) LoadWindows(ctx context.Context, request LoadWindowsRequest) (result LoadWindowsResult, returnErr error) {
	started := time.Now()
	observation := Observation{
		Stage: StageDependencyLoaded, Result: OperationSucceeded, Codec: CodecNoneV1,
		TouchedKeys: len(request.Items),
	}
	defer func() {
		store.finishLoadObservation(ctx, &observation, result, returnErr, time.Since(started))
	}()
	result = LoadWindowsResult{Items: make([]LoadedWindow, len(request.Items))}
	groups := make(map[string][]routedLoad)
	order := make([]string, 0)
	seenKeys := make(map[string]struct{}, len(request.Items))
	for index, spec := range request.Items {
		key, err := spec.Identity.Key(store.options.Prefix)
		if err != nil {
			result.Items[index] = LoadedWindow{
				Identity: spec.Identity, Requirements: append([]LevelRequirement(nil), spec.Requirements...),
				Status: LoadUnavailable, Err: fmt.Errorf("state: load item %d key: %w", index, err),
			}
			continue
		}
		if _, duplicate := seenKeys[key]; duplicate {
			return result, fmt.Errorf("state: load item %d duplicates RuntimeKey", index)
		}
		seenKeys[key] = struct{}{}
		target, err := store.options.Router.Route(spec.Identity.TenantID, spec.Identity.StrategyID)
		if err != nil {
			return result, fmt.Errorf("state: route strategy %s: %w", spec.Identity.StrategyID, err)
		}
		if target.Name == "" || target.Backend == nil {
			return result, fmt.Errorf("state: route strategy %s returned invalid target", spec.Identity.StrategyID)
		}
		observation.Target = mergeObservationTarget(observation.Target, target.Name)
		if _, exists := groups[target.Name]; !exists {
			order = append(order, target.Name)
		}
		groups[target.Name] = append(groups[target.Name], routedLoad{index: index, key: key, target: target, spec: spec})
	}
	loadedBytes := 0
	for _, targetName := range order {
		items := groups[targetName]
		for start := 0; start < len(items); {
			end, err := store.loadBatchEnd(items, start, store.options.Limits.MaxLoadedBytes-loadedBytes)
			if err != nil {
				return result, err
			}
			keys := make([]string, end-start)
			for index := start; index < end; index++ {
				keys[index-start] = items[index].key
			}
			observation.BackendCalls++
			observation.RequestBytes += stringsBytes(keys)
			values, err := items[start].target.Backend.MGet(ctx, keys)
			if err != nil {
				observation.ReasonCode = contract.ReasonRedisUnavailable
				return result, fmt.Errorf("state: load target %s: %w", targetName, err)
			}
			responseBytes := byteSlicesBytes(values)
			observation.ResponseBytes += responseBytes
			observation.DecodeBytes += responseBytes
			if len(values) != len(keys) {
				return result, fmt.Errorf("state: load target %s returned %d values for %d keys", targetName, len(values), len(keys))
			}
			for index, value := range values {
				loadedBytes += len(value)
				if loadedBytes > store.options.Limits.MaxLoadedBytes {
					return result, fmt.Errorf("%w: loaded bytes", ErrStateBudget)
				}
				item := items[start+index]
				result.Items[item.index] = store.decodeLoaded(item, value)
			}
			start = end
		}
	}
	return result, nil
}

func (store *Store) WriteWindows(ctx context.Context, request WriteWindowsRequest) (result WriteWindowsResult, returnErr error) {
	started := time.Now()
	observation := Observation{
		Stage: StageStateCommitted, Result: OperationSucceeded, Codec: CodecNoneV1,
		TouchedKeys: len(request.Items),
	}
	defer func() {
		store.finishWriteObservation(ctx, &observation, result, returnErr, time.Since(started))
	}()
	result = WriteWindowsResult{Items: make([]WriteWindowResult, len(request.Items))}
	groups := make(map[string][]routedWrite)
	order := make([]string, 0)
	writtenBytes := 0
	seenKeys := make(map[string]struct{}, len(request.Items))
	for index := range request.Items {
		item := &request.Items[index]
		result.Items[index].Key = item.Key
		expectedKey, err := item.Identity.Key(store.options.Prefix)
		if err != nil {
			return result, fmt.Errorf("state: write item %d key: %w", index, err)
		}
		if item.Key != expectedKey {
			return result, fmt.Errorf("state: write item %d physical key mismatch", index)
		}
		if _, duplicate := seenKeys[item.Key]; duplicate {
			return result, fmt.Errorf("state: write item %d duplicates RuntimeKey", index)
		}
		seenKeys[item.Key] = struct{}{}
		if item.Window == nil {
			result.Items[index].Status = WriteUnavailable
			result.Items[index].Err = item.Err
			continue
		}
		if !item.Window.Changed() {
			result.Items[index].Status = WriteNoop
			continue
		}
		upperBound, err := store.options.Codec.AdmitWindow(item.Window)
		if err != nil {
			result.Items[index].Status = WriteInvariantViolation
			result.Items[index].Err = err
			continue
		}
		if upperBound > store.options.Limits.MaxWrittenBytes-writtenBytes {
			return result, fmt.Errorf("%w: remaining written bytes cannot admit state", ErrStateBudget)
		}
		value, err := store.options.Codec.Encode(item.Window)
		if err != nil {
			result.Items[index].Status = WriteInvariantViolation
			result.Items[index].Err = err
			continue
		}
		ttl, err := StateTTL(item.Requirements, store.options.RestartMargin, store.options.MinTTL, store.options.MaxTTL)
		if err != nil {
			result.Items[index].Status = WriteInvariantViolation
			result.Items[index].Err = err
			continue
		}
		writtenBytes += len(value)
		observation.EncodeBytes += len(value)
		observation.StateBytes += len(value)
		if writtenBytes > store.options.Limits.MaxWrittenBytes {
			return result, fmt.Errorf("%w: written bytes", ErrStateBudget)
		}
		target, err := store.options.Router.Route(item.Identity.TenantID, item.Identity.StrategyID)
		if err != nil {
			return result, fmt.Errorf("state: route strategy %s: %w", item.Identity.StrategyID, err)
		}
		if target.Name == "" || target.Backend == nil {
			return result, fmt.Errorf("state: route strategy %s returned invalid target", item.Identity.StrategyID)
		}
		observation.Target = mergeObservationTarget(observation.Target, target.Name)
		if _, exists := groups[target.Name]; !exists {
			order = append(order, target.Name)
		}
		groups[target.Name] = append(groups[target.Name], routedWrite{
			index: index, target: target, window: item.Window,
			write: BackendWrite{Key: item.Key, Value: value, TTL: ttl},
		})
		result.Items[index].EncodedBytes = len(value)
		result.Items[index].TTL = ttl
	}
	for _, targetName := range order {
		items := groups[targetName]
		for start := 0; start < len(items); {
			end, err := store.writeBatchEnd(items, start)
			if err != nil {
				return result, err
			}
			writes := make([]BackendWrite, end-start)
			for index := start; index < end; index++ {
				writes[index-start] = items[index].write
			}
			observation.BackendCalls++
			observation.RequestBytes += writesBytes(writes)
			if err := items[start].target.Backend.SetMany(ctx, writes); err != nil {
				observation.ReasonCode = contract.ReasonStateWriteRetryable
				return result, fmt.Errorf("state: write target %s: %w", targetName, err)
			}
			for index := start; index < end; index++ {
				items[index].window.MarkPersisted()
				result.Items[items[index].index].Status = WritePersisted
			}
			start = end
		}
	}
	return result, nil
}

func (store *Store) finishLoadObservation(
	ctx context.Context, observation *Observation, result LoadWindowsResult, returnErr error, duration time.Duration,
) {
	classified := 0
	for _, item := range result.Items {
		switch item.Status {
		case LoadFound:
			observation.FoundKeys++
			classified++
		case LoadMissing:
			observation.MissingKeys++
			classified++
		case LoadResetCorrupt:
			observation.ResetCorruptKeys++
			classified++
		case LoadUnavailable:
			classified++
			if errors.Is(item.Err, ErrUnsupportedState) {
				observation.UnsupportedKeys++
			} else if errors.Is(item.Err, ErrStateBudget) {
				observation.UnavailableKeys++
				observation.BudgetViolations++
			} else {
				observation.UnavailableKeys++
				observation.InvariantViolations++
			}
		}
	}
	if returnErr != nil {
		observation.Result = OperationFailed
		observation.UnavailableKeys += observation.TouchedKeys - classified
	} else if observation.ResetCorruptKeys > 0 || observation.UnsupportedKeys > 0 || observation.UnavailableKeys > 0 {
		observation.Result = OperationPartial
	}
	observation.Duration = duration
	observeState(ctx, store.options.Observer, *observation)
}

func (store *Store) finishWriteObservation(
	ctx context.Context, observation *Observation, result WriteWindowsResult, returnErr error, duration time.Duration,
) {
	classified := 0
	for _, item := range result.Items {
		switch item.Status {
		case WriteNoop:
			observation.NoopKeys++
			classified++
		case WritePersisted:
			observation.PersistedKeys++
			classified++
		case WriteUnavailable:
			classified++
			if errors.Is(item.Err, ErrUnsupportedState) {
				observation.UnsupportedKeys++
			} else {
				observation.UnavailableKeys++
			}
		case WriteInvariantViolation:
			observation.InvariantKeys++
			classified++
			if errors.Is(item.Err, ErrStateBudget) {
				observation.BudgetViolations++
			} else {
				observation.InvariantViolations++
			}
		}
	}
	if returnErr != nil {
		observation.Result = OperationFailed
		observation.UnavailableKeys += observation.TouchedKeys - classified
	} else if observation.InvariantKeys > 0 || observation.UnsupportedKeys > 0 || observation.UnavailableKeys > 0 {
		observation.Result = OperationFailed
	}
	observation.Duration = duration
	observeState(ctx, store.options.Observer, *observation)
}

func mergeObservationTarget(current, next string) string {
	if current == "" || current == next {
		return next
	}
	return "MULTIPLE"
}

func stringsBytes(values []string) int {
	total := 0
	for _, value := range values {
		total += len(value)
	}
	return total
}

func byteSlicesBytes(values [][]byte) int {
	total := 0
	for _, value := range values {
		total += len(value)
	}
	return total
}

func writesBytes(writes []BackendWrite) int {
	total := 0
	for _, write := range writes {
		total += len(write.Key) + len(write.Value)
	}
	return total
}

func StateTTL(requirements []LevelRequirement, restartMargin, minimum, maximum time.Duration) (time.Duration, error) {
	if len(requirements) == 0 || restartMargin < 0 || minimum <= 0 || maximum < minimum {
		return 0, fmt.Errorf("state: invalid TTL inputs")
	}
	var required time.Duration
	for _, requirement := range requirements {
		if requirement.RetentionPoints == 0 || requirement.EvaluationInterval <= 0 || requirement.LatenessTolerance < 0 {
			return 0, fmt.Errorf("state: invalid Level %d TTL requirement", requirement.LevelID)
		}
		if uint64(requirement.RetentionPoints) > uint64(math.MaxInt64/int64(requirement.EvaluationInterval)) {
			return 0, fmt.Errorf("%w: Level %d TTL overflow", ErrStateBudget, requirement.LevelID)
		}
		retention := time.Duration(requirement.RetentionPoints) * requirement.EvaluationInterval
		if retention > time.Duration(math.MaxInt64)-requirement.LatenessTolerance ||
			retention+requirement.LatenessTolerance > time.Duration(math.MaxInt64)-restartMargin {
			return 0, fmt.Errorf("%w: Level %d TTL overflow", ErrStateBudget, requirement.LevelID)
		}
		candidate := retention + requirement.LatenessTolerance + restartMargin
		if candidate > required {
			required = candidate
		}
	}
	if required < minimum {
		required = minimum
	}
	if required > maximum {
		return 0, fmt.Errorf("%w: required TTL %s exceeds maximum %s", ErrStateBudget, required, maximum)
	}
	return required, nil
}

func (store *Store) decodeLoaded(item routedLoad, value []byte) LoadedWindow {
	loaded := LoadedWindow{
		Identity: item.spec.Identity, Key: item.key,
		Requirements: append([]LevelRequirement(nil), item.spec.Requirements...),
	}
	if value == nil {
		window, err := NewWindow(item.spec.Requirements)
		window.setObserver(store.options.Observer)
		loaded.Window, loaded.Err = window, err
		if err != nil {
			loaded.Status = LoadUnavailable
		} else {
			loaded.Status = LoadMissing
		}
		return loaded
	}
	window, err := store.options.Codec.Decode(value)
	if err == nil {
		err = window.Align(item.spec.Requirements)
	}
	if err == nil {
		window.setObserver(store.options.Observer)
		loaded.Window, loaded.Status = window, LoadFound
		return loaded
	}
	loaded.Err = err
	if errors.Is(err, ErrCorruptState) {
		window, createErr := NewWindow(item.spec.Requirements)
		if createErr != nil {
			loaded.Err = errors.Join(err, createErr)
			loaded.Status = LoadUnavailable
			return loaded
		}
		window.setObserver(store.options.Observer)
		loaded.Window, loaded.Status = window, LoadResetCorrupt
		return loaded
	}
	loaded.Status = LoadUnavailable
	return loaded
}

func (store *Store) loadBatchEnd(items []routedLoad, start, remainingLoadedBytes int) (int, error) {
	maxValueBytes := store.options.Codec.limits.MaxEncodedBytes
	if remainingLoadedBytes < maxValueBytes {
		return 0, fmt.Errorf("%w: remaining loaded bytes cannot admit one state", ErrStateBudget)
	}
	end, keyBytes := start, 0
	for end < len(items) && end-start < store.options.Limits.MaxKeysPerBatch {
		if len(items[end].key) > store.options.Limits.MaxKeyBytesPerBatch {
			return 0, fmt.Errorf("%w: key bytes", ErrStateBudget)
		}
		if end > start && keyBytes+len(items[end].key) > store.options.Limits.MaxKeyBytesPerBatch {
			break
		}
		if end-start+1 > remainingLoadedBytes/maxValueBytes {
			break
		}
		keyBytes += len(items[end].key)
		end++
	}
	return end, nil
}

func (store *Store) writeBatchEnd(items []routedWrite, start int) (int, error) {
	end, keyBytes := start, 0
	for end < len(items) && end-start < store.options.Limits.MaxKeysPerBatch {
		if len(items[end].write.Key) > store.options.Limits.MaxKeyBytesPerBatch {
			return 0, fmt.Errorf("%w: key bytes", ErrStateBudget)
		}
		if end > start && keyBytes+len(items[end].write.Key) > store.options.Limits.MaxKeyBytesPerBatch {
			break
		}
		keyBytes += len(items[end].write.Key)
		end++
	}
	return end, nil
}
