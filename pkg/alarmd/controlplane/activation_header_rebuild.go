// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

// ErrActivationHeaderMissing is a renewal that found the activation body and
// no header. Every guarded write - the renewal and every cutover - compares
// the header it read with the one in Redis, and an absent header matches no
// expectation, so without a rebuild each of them is refused on every round
// for as long as the header stays gone: no strategy change is activated and
// nothing the current activation names is renewed, until its objects expire
// and the fleet executes nothing. Readers are unaffected - they read the body
// - which is why it has to be named: nothing else fails.
//
// It is not an ErrActivationConflict. A conflict is another writer's header,
// which the next round reads and moves on from; this is no writer's header,
// which no round moves on from by itself.
var ErrActivationHeaderMissing = errors.New("alarmd controlplane: activation header missing, body present")

// ErrActivationHeaderRebuildRefused is the error of every header rebuild
// outcome that wrote nothing for a reason a retry will not change by itself.
var ErrActivationHeaderRebuildRefused = errors.New("alarmd controlplane: activation header cannot be rebuilt")

// ActivationHeaderRebuildOutcome is how one header rebuild that found the
// header missing ended. Closed: a metric label.
type ActivationHeaderRebuildOutcome string

const (
	// ActivationHeaderRebuilt: the header was written back as the body
	// describes it.
	ActivationHeaderRebuilt ActivationHeaderRebuildOutcome = "rebuilt"
	// ActivationHeaderRebuildConflict: a header appeared, or the body
	// changed, between the read and the write. Nothing was written; the next
	// round reads again.
	ActivationHeaderRebuildConflict ActivationHeaderRebuildOutcome = "conflict"
	// ActivationHeaderRebuildBodyUnparsable: the body is not one this build
	// reads, so what it describes is not known and no header is guessed.
	ActivationHeaderRebuildBodyUnparsable ActivationHeaderRebuildOutcome = "body_unparsable"
	// ActivationHeaderRebuildBodyPending: the body names a pending
	// publication, which no writer of this build leaves behind; see
	// RebuildActivationHeader.
	ActivationHeaderRebuildBodyPending ActivationHeaderRebuildOutcome = "body_pending"
)

// ActivationHeaderRebuildNotNeeded is what a rebuild that found nothing to do
// returns: the header was there, or the body was gone too, which is the first
// activation's case. It is not counted - every activation round asks - and
// so is not among ActivationHeaderRebuildOutcomes.
const ActivationHeaderRebuildNotNeeded ActivationHeaderRebuildOutcome = "not_needed"

// ActivationHeaderRebuildOutcomes lists every counted outcome, for the metric
// that pre-creates them all.
var ActivationHeaderRebuildOutcomes = []ActivationHeaderRebuildOutcome{
	ActivationHeaderRebuilt, ActivationHeaderRebuildConflict, ActivationHeaderRebuildBodyUnparsable,
	ActivationHeaderRebuildBodyPending,
}

// ActivationRenewalConflict is why a renewal of the current activation's
// objects wrote nothing. Closed: a metric label.
type ActivationRenewalConflict string

const (
	// ActivationRenewalHeaderMissing: no header at all; see
	// ErrActivationHeaderMissing.
	ActivationRenewalHeaderMissing ActivationRenewalConflict = "header_missing"
	// ActivationRenewalHeaderMoved: another header than the one read - a
	// cutover between the read and the renewal. The next round renews
	// what that cutover named.
	ActivationRenewalHeaderMoved ActivationRenewalConflict = "header_moved"
)

// ActivationRenewalConflicts lists every renewal conflict.
var ActivationRenewalConflicts = []ActivationRenewalConflict{ActivationRenewalHeaderMissing, ActivationRenewalHeaderMoved}

// activationHeaderStanding is what this process has seen of a missing
// header: since when, the last rebuild outcome, and the counts. Only the
// Control Leader writes it; every other replica reads the body and never
// looks.
type activationHeaderStanding struct {
	mu        sync.Mutex
	since     time.Time
	last      ActivationHeaderRebuildOutcome
	rebuilds  map[ActivationHeaderRebuildOutcome]uint64
	conflicts map[ActivationRenewalConflict]uint64
}

func (standing *activationHeaderStanding) missing(at time.Time) {
	standing.mu.Lock()
	defer standing.mu.Unlock()
	if standing.since.IsZero() {
		standing.since = at
	}
}

func (standing *activationHeaderStanding) present() {
	standing.mu.Lock()
	defer standing.mu.Unlock()
	standing.since, standing.last = time.Time{}, ""
}

func (standing *activationHeaderStanding) rebuilt(outcome ActivationHeaderRebuildOutcome) {
	standing.mu.Lock()
	defer standing.mu.Unlock()
	if standing.rebuilds == nil {
		standing.rebuilds = make(map[ActivationHeaderRebuildOutcome]uint64, len(ActivationHeaderRebuildOutcomes))
	}
	standing.rebuilds[outcome]++
	standing.last = outcome
	if outcome == ActivationHeaderRebuilt {
		standing.since = time.Time{}
	}
}

func (standing *activationHeaderStanding) conflict(reason ActivationRenewalConflict) {
	standing.mu.Lock()
	defer standing.mu.Unlock()
	if standing.conflicts == nil {
		standing.conflicts = make(map[ActivationRenewalConflict]uint64, len(ActivationRenewalConflicts))
	}
	standing.conflicts[reason]++
}

// ActivationHeaderReading is the Control Leader's standing on the activation
// header. Missing is set from the round that found the header gone until one
// found it back; LastRebuild is the outcome of the last attempt to write it
// back while it was gone.
type ActivationHeaderReading struct {
	Missing          bool
	MissingSince     time.Time
	LastRebuild      ActivationHeaderRebuildOutcome
	Rebuilds         map[ActivationHeaderRebuildOutcome]uint64
	RenewalConflicts map[ActivationRenewalConflict]uint64
}

// ActivationHeaderReading is every count, zero included, and the standing.
func (repository *RedisCatalogRepository) ActivationHeaderReading() ActivationHeaderReading {
	reading := ActivationHeaderReading{
		Rebuilds:         make(map[ActivationHeaderRebuildOutcome]uint64, len(ActivationHeaderRebuildOutcomes)),
		RenewalConflicts: make(map[ActivationRenewalConflict]uint64, len(ActivationRenewalConflicts)),
	}
	if repository == nil {
		return reading
	}
	standing := &repository.header
	standing.mu.Lock()
	defer standing.mu.Unlock()
	for _, outcome := range ActivationHeaderRebuildOutcomes {
		reading.Rebuilds[outcome] = standing.rebuilds[outcome]
	}
	for _, reason := range ActivationRenewalConflicts {
		reading.RenewalConflicts[reason] = standing.conflicts[reason]
	}
	reading.Missing, reading.MissingSince, reading.LastRebuild = !standing.since.IsZero(), standing.since, standing.last
	return reading
}

// rebuildActivationHeaderScript writes the header back only while there is
// none and the body is the one the header was computed from. Nothing else is
// touched: the body, the timelines and the revision stay as they are, so the
// header names exactly what is already there.
const rebuildActivationHeaderScript = `
if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
local body = redis.call('GET', KEYS[2])
if not body or redis.sha1hex(body) ~= ARGV[2] then return 0 end
redis.call('SET', KEYS[1], ARGV[1])
return 1
`

// RebuildActivationHeader writes back the activation header when the body is
// present without it. Only the Control Leader calls it - at the start of every
// activation and when its renewal finds the header gone - which is the fence
// RebuildActivationBody has too; the script's own condition is what makes a
// second writer harmless: the header is a function of the body, so two
// leaders racing would write the same bytes, and the one that comes second
// finds a header and writes nothing.
//
// The header is computed from the body and from nothing else: the same
// record revision, the same current publication, no new revision or epoch.
// Its pending part is "-". No writer of this build leaves a pending
// publication behind - a cutover commits the whole move in one script - so a
// header this build wrote named none, and the body of such an activation
// names none either; one that does came from a writer this build does not
// know and is refused rather than guessed. A cutover a later build left
// unfinished in pieces is on the body (CutoverProgress), not on the header,
// so the header written back is the one it had, and the next activation
// round finishes it as it would have.
//
// With the header and the body both gone there is nothing to describe: that
// is the first activation's case, as it always was.
func (repository *RedisCatalogRepository) RebuildActivationHeader(ctx context.Context) (ActivationHeaderRebuildOutcome, error) {
	if repository == nil || repository.client == nil {
		return "", errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	version, err := repository.fetchControlVersion(ctx)
	if err != nil {
		return "", err
	}
	if version.known {
		repository.header.present()
		return ActivationHeaderRebuildNotNeeded, nil
	}
	payload, err := repository.client.Get(ctx, repository.activationKey()).Result()
	if errors.Is(err, redis.Nil) {
		repository.header.present()
		return ActivationHeaderRebuildNotNeeded, nil
	}
	if err != nil {
		return "", activationDependencyIO(fmt.Errorf("read activation body for its header: %w", err))
	}
	repository.header.missing(time.Now())
	outcome, err := repository.writeActivationHeader(ctx, payload)
	if outcome != "" {
		repository.header.rebuilt(outcome)
	}
	if outcome == ActivationHeaderRebuilt {
		repository.clearActivationCaches()
	}
	return outcome, err
}

func (repository *RedisCatalogRepository) writeActivationHeader(ctx context.Context, payload string) (ActivationHeaderRebuildOutcome, error) {
	entry, err := parseActivation(payload)
	if err != nil {
		return ActivationHeaderRebuildBodyUnparsable, fmt.Errorf("%w: %v", ErrActivationHeaderRebuildRefused, err)
	}
	if entry.state.Pending != nil {
		return ActivationHeaderRebuildBodyPending, fmt.Errorf("%w: the body names a pending publication", ErrActivationHeaderRebuildRefused)
	}
	header, err := activationHeader(entry.state.RecordRevision, entry.state.Current, nil)
	if err != nil || header == "" {
		return ActivationHeaderRebuildBodyUnparsable, fmt.Errorf("%w: the body names no activation", ErrActivationHeaderRebuildRefused)
	}
	sum := sha1.Sum([]byte(payload))
	changed, err := repository.client.Eval(ctx, rebuildActivationHeaderScript,
		[]string{repository.activationHeaderKey(), repository.activationKey()}, header, hex.EncodeToString(sum[:])).Int()
	if err != nil {
		return "", activationDependencyIO(fmt.Errorf("rebuild activation header: %w", err))
	}
	if changed != 1 {
		return ActivationHeaderRebuildConflict, nil
	}
	return ActivationHeaderRebuilt, nil
}
