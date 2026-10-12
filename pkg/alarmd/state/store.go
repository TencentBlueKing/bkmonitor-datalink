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
	"fmt"
	"math"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
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
	// Targets lists every target Route can return. A store checks the
	// capabilities it needs against this list when it opens, so a backend
	// that lacks one is refused as wiring rather than met on a Slot as a
	// refusal that recurs every round. Required, not an optional extension:
	// a router that could not be listed could not be checked, and the
	// check is the point.
	Targets() []StorageTarget
}

type StoreLimits struct {
	MaxKeysPerBatch     int
	MaxKeyBytesPerBatch int
	MaxLoadedBytes      int
	MaxWrittenBytes     int
}

func StateTTL(requirements []LevelRequirement, restartMargin, minimum, maximum time.Duration, readHoldBound ...time.Duration) (time.Duration, error) {
	var hold time.Duration
	if len(readHoldBound) > 1 {
		return 0, fmt.Errorf("state: invalid read hold lifetime")
	}
	if len(readHoldBound) == 1 {
		hold = readHoldBound[0]
	}
	if hold < 0 || hold.Milliseconds() > execution.MaxReadHoldMillis {
		return 0, fmt.Errorf("state: invalid read hold lifetime")
	}
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
	if required > maximum {
		return 0, fmt.Errorf("%w: required TTL %s exceeds maximum %s", ErrStateBudget, required, maximum)
	}
	if required < minimum {
		required = minimum
	}
	// The offset goes on after both bounds, so that neither bound can undo
	// it. Before the floor, a short retention clamped up to a whole-minute
	// floor lost the offset and kept the phase; before the ceiling, a
	// retention that fit the ceiling exactly was pushed over it and refused,
	// and a Plan refused here is a Plan that stops remembering. Under the
	// ceiling the offset steps back to the previous half step instead: still
	// past the horizon the TTL was derived to outlive, half a step less of the
	// restart margin, and the Plan keeps its memory.
	offset := offsetFromTheStep(required, requirements)
	if offset > maximum {
		if step := longestStep(requirements); step > 0 && offset-step >= minimum {
			offset -= step
		} else {
			offset = required
		}
	}
	// The read hold only lengthens the key's life past a horizon the offset
	// already outlives; near the ceiling it gets what is left below it. The
	// retention fits without it, and refusing it here refused, every round,
	// a Plan that fits -- one that would then stop remembering.
	return min(offset+hold, maximum), nil
}

// longestStep is the step the offset keeps the expiry away from: the longest
// interval among the requirements. The compiler holds every Level of a Plan to
// the Plan's own interval, so there is one.
func longestStep(requirements []LevelRequirement) time.Duration {
	var step time.Duration
	for _, requirement := range requirements {
		if requirement.EvaluationInterval > step {
			step = requirement.EvaluationInterval
		}
	}
	return step
}

// offsetFromTheStep moves a TTL to the next duration that sits half a step
// past a whole number of steps, so a key never expires at the phase its Slot
// runs at.
//
// A Slot runs at a fixed offset inside its step and writes its keys at that
// offset every round, so a key's expiry -- one write plus the TTL -- lands at
// the same offset some rounds later. A TTL that is a whole number of steps
// puts the expiry exactly where the round's write happens: a series that
// returns after exactly that many rounds is read a few seconds before its key
// dies and written a few seconds after, and the write finds no key. That was
// most of what a fleet read as STATE_VERSION_CONFLICT/missing: not a
// competing writer, the clock. Half a step away from the write phase, the
// expiry falls between two rounds for every Slot whose read-to-write span is
// under half a step, whatever phase the Slot runs at; the cost is at most one
// step of extra life per key, and less than 3% on the deployment it was
// measured on.
//
// Generation-scoped keys do not need it -- they are renewed on every read and
// nothing writes them at a Slot's phase -- but they get it where their life is
// the runtime TTL: GenerationScopedTTL takes this TTL, offset included, when
// it is longer than the whole-day floor, and the floor itself when not. The
// offset only lengthens a life, so on those keys it costs at most one step.
func offsetFromTheStep(ttl time.Duration, requirements []LevelRequirement) time.Duration {
	step := longestStep(requirements)
	if step <= 0 {
		return ttl
	}
	half := step / 2
	remainder := ttl % step
	if remainder == half {
		return ttl
	}
	advance := half - remainder
	if advance < 0 {
		advance += step
	}
	if ttl > time.Duration(math.MaxInt64)-advance {
		return ttl
	}
	return ttl + advance
}
