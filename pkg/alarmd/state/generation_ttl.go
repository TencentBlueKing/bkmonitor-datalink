// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import "time"

// GenerationScopedFloor is the shortest life a generation-scoped key may have.
//
// A generation-scoped key carries the state generation in its name, so a Plan
// whose execution content changes leaves its old key behind: never read again,
// and until now never expired either. The floor is what turns that leak into a
// wait.
//
// One day, because that is the backend's own forgetting period. Python stores
// the no-data history dimensions under a key with a one-day TTL, so a check
// that does not run for a day forgets which groups it used to see and rebuilds
// the roster from what reports next. Its checkpoints have no TTL at all, but
// they are keyed by strategy and item and overwritten in place, so they never
// orphan - the generation in alarmd's key is the only reason these do.
//
// So the floor is the span after which the backend would have forgotten anyway.
// A stop longer than this - an outage, or a rollback to a build that does not
// recognise the record - loses the history roster and restarts FirstAbsent,
// which is what Python does after a day off.
const GenerationScopedFloor = 24 * time.Hour

// GenerationScopedTTL is how long a key that names a state generation lives.
//
// It is the runtime state's own TTL when that is longer, so a Plan whose window
// reaches back further than a day keeps its generation-scoped keys at least as
// long as the series they describe. Anything shorter would expire the memory of
// an absence while the window that absence is judged against is still alive.
//
// An empty retention is the floor rather than an error: the caller is saying it
// has no Plan-specific need, and the floor is the safe direction - a key that
// lives too long is a byte of waste, and one that expires too early restarts an
// absence clock that was still running.
func GenerationScopedTTL(
	requirements []LevelRequirement, restartMargin, minimum, maximum time.Duration,
) (time.Duration, error) {
	if len(requirements) == 0 {
		return GenerationScopedFloor, nil
	}
	runtime, err := StateTTL(requirements, restartMargin, minimum, maximum)
	if err != nil {
		return 0, err
	}
	if runtime < GenerationScopedFloor {
		return GenerationScopedFloor, nil
	}
	return runtime, nil
}

// GenerationScopedRenewalThreshold is the remaining life below which a loaded
// key is renewed.
//
// Half, so a key is rewritten about once per half-life rather than once per
// Slot: a Plan on a one-minute interval with a one-day life sets a new expiry
// every twelve hours instead of seven hundred times a day.
//
// The threshold bounds the writes and nothing else. It cannot bound the round
// trips, because it is evaluated inside Redis: a load that reaches the script
// has already spent the trip, whatever the script then decides. That was taken
// for small beside the same Slot's series reads and measured otherwise -- 38.6
// EVAL/s at 7.1ms each on around 2,100 Plans, which is 0.27 seconds of waiting
// on Redis every second, and it doubles once a Plan loads its no-data memory
// as well as its gap marker. What bounds the trips is the ask gate, on the
// client; see RenewalAskInterval.
func GenerationScopedRenewalThreshold(ttl time.Duration) time.Duration {
	return ttl / 2
}
