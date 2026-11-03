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
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ErrActivationBodyMissing is an activation whose header is present and whose
// body is not: evicted, or lost in a failover that kept only one of the two
// keys. It is an ErrActivationUnavailable to every reader, which cannot act on
// the difference; the Control Leader can, and rebuilds the body (see
// RebuildActivationBody). The header alone - no body - is left to the first
// activation, as before.
var ErrActivationBodyMissing = fmt.Errorf("%w: header present, body missing", ErrActivationUnavailable)

// ActivationRebuildOutcome is how one rebuild ended. Closed: a metric label.
type ActivationRebuildOutcome string

const (
	// ActivationRebuilt: the body was written back, exactly as the leader
	// last read it under the same header.
	//
	// Either rebuilt outcome may leave Query Groups of the publication whose
	// timeline is gone or does not decode unread: they keep their place - in
	// the active set, or in Draining where the copy last read held them - so
	// that no reader takes them for Query Groups that left, and they are
	// counted by reason (ActivationRebuildUnreadCounts). The activation read
	// names them in SkippedTimelines, and Ensure gives them a timeline again.
	ActivationRebuilt ActivationRebuildOutcome = "rebuilt"
	// ActivationRebuiltDrainingUnknown: the body was rebuilt from the open
	// Segments without a last-good copy. The Plans are exact; Draining holds
	// only the Query Groups the publication keeps held, so Query Groups a
	// cutover removed and that had not drained yet stop executing now rather
	// than at their boundary: their Plans have no owner until then, and that
	// stretch shows up as a gap. A reader of this count checks the gaps.
	// A Query Group of the publication whose timeline is unread cannot say
	// whether it was held either, and is kept in the active set.
	ActivationRebuiltDrainingUnknown ActivationRebuildOutcome = "rebuilt_draining_unknown"
	// ActivationRebuildNotNeeded: the body was back, or the header gone, by
	// the time the rebuild looked.
	ActivationRebuildNotNeeded ActivationRebuildOutcome = "not_needed"
	// ActivationRebuildConflict: another writer moved the header, a body, or
	// a timeline between the read and the write. Nothing was written.
	ActivationRebuildConflict ActivationRebuildOutcome = "conflict"
	// ActivationRebuildCoverageInvalid: the candidate body and the open
	// Segments disagree. Nothing that disagrees with the Segments is written.
	ActivationRebuildCoverageInvalid ActivationRebuildOutcome = "coverage_invalid"
	// ActivationRebuildHeaderUnparsable: the header is not one this build
	// writes.
	ActivationRebuildHeaderUnparsable ActivationRebuildOutcome = "header_unparsable"
	// ActivationRebuildHeaderPending: the header names a pending publication,
	// which no writer of this build leaves behind; the body is not guessed.
	ActivationRebuildHeaderPending ActivationRebuildOutcome = "header_pending"
)

// ActivationRebuildOutcomes lists every outcome, for the metric that
// pre-creates them all.
var ActivationRebuildOutcomes = []ActivationRebuildOutcome{
	ActivationRebuilt, ActivationRebuiltDrainingUnknown, ActivationRebuildNotNeeded, ActivationRebuildConflict,
	ActivationRebuildCoverageInvalid, ActivationRebuildHeaderUnparsable, ActivationRebuildHeaderPending,
}

// ErrActivationRebuildRefused is the error of every outcome that wrote
// nothing for a reason a retry will not change by itself.
var ErrActivationRebuildRefused = errors.New("alarmd controlplane: activation body cannot be rebuilt")

// lastGoodActivation is the activation this process last served under a
// known header. The body cache is dropped the moment a read finds the body
// gone - a cache must not outlive the object it caches - so the copy the
// rebuild needs is kept apart from it. It is only ever written back under the
// same header it was read under.
type lastGoodActivation struct {
	mu     sync.Mutex
	header string
	entry  *parsedActivation
}

func (last *lastGoodActivation) remember(header string, entry *parsedActivation) {
	if header == "" || entry == nil {
		return
	}
	last.mu.Lock()
	last.header, last.entry = header, entry
	last.mu.Unlock()
}

func (last *lastGoodActivation) under(header string) (*parsedActivation, bool) {
	last.mu.Lock()
	defer last.mu.Unlock()
	if last.entry == nil || last.header != header {
		return nil, false
	}
	return last.entry, true
}

// activationRebuildCounts is every rebuild this process attempted, by how it
// ended, and every Query Group a rebuild it wrote left unread, by why; read
// at scrape time.
type activationRebuildCounts struct {
	mu     sync.Mutex
	counts map[ActivationRebuildOutcome]uint64
	unread map[SkippedTimelineReason]uint64
}

func (counts *activationRebuildCounts) addUnread(unread []SkippedTimeline) {
	if len(unread) == 0 {
		return
	}
	counts.mu.Lock()
	defer counts.mu.Unlock()
	if counts.unread == nil {
		counts.unread = make(map[SkippedTimelineReason]uint64, len(SkippedTimelineReasons))
	}
	for _, entry := range unread {
		counts.unread[entry.Reason]++
	}
}

func (counts *activationRebuildCounts) add(outcome ActivationRebuildOutcome) {
	counts.mu.Lock()
	defer counts.mu.Unlock()
	if counts.counts == nil {
		counts.counts = make(map[ActivationRebuildOutcome]uint64, len(ActivationRebuildOutcomes))
	}
	counts.counts[outcome]++
}

// ActivationRebuildCounts is every outcome, zero included.
func (repository *RedisCatalogRepository) ActivationRebuildCounts() map[ActivationRebuildOutcome]uint64 {
	result := make(map[ActivationRebuildOutcome]uint64, len(ActivationRebuildOutcomes))
	if repository == nil {
		return result
	}
	repository.rebuilds.mu.Lock()
	defer repository.rebuilds.mu.Unlock()
	for _, outcome := range ActivationRebuildOutcomes {
		result[outcome] = repository.rebuilds.counts[outcome]
	}
	return result
}

// ActivationRebuildUnreadCounts is every Query Group a written rebuild kept
// without reading its timeline, by reason, zero included.
func (repository *RedisCatalogRepository) ActivationRebuildUnreadCounts() map[SkippedTimelineReason]uint64 {
	result := make(map[SkippedTimelineReason]uint64, len(SkippedTimelineReasons))
	if repository == nil {
		return result
	}
	repository.rebuilds.mu.Lock()
	defer repository.rebuilds.mu.Unlock()
	for _, reason := range SkippedTimelineReasons {
		result[reason] = repository.rebuilds.unread[reason]
	}
	return result
}

// parseActivationHeader reads what activationHeader writes:
// "<revision>|<snapshot>@<epoch>|<pending snapshot>@<epoch> or ->".
func parseActivationHeader(header string) (uint64, SnapshotPublicationRef, *SnapshotPublicationRef, error) {
	parts := strings.Split(header, "|")
	if len(parts) != 3 {
		return 0, SnapshotPublicationRef{}, nil, errors.New("activation header has no three parts")
	}
	revision, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || revision == 0 {
		return 0, SnapshotPublicationRef{}, nil, errors.New("activation header revision is invalid")
	}
	current, err := parsePublicationRef(parts[1])
	if err != nil {
		return 0, SnapshotPublicationRef{}, nil, err
	}
	if parts[2] == "-" {
		return revision, current, nil, nil
	}
	pending, err := parsePublicationRef(parts[2])
	if err != nil {
		return 0, SnapshotPublicationRef{}, nil, err
	}
	return revision, current, &pending, nil
}

func parsePublicationRef(text string) (SnapshotPublicationRef, error) {
	at := strings.LastIndexByte(text, '@')
	if at <= 0 {
		return SnapshotPublicationRef{}, errors.New("activation header publication is invalid")
	}
	epoch, err := strconv.ParseUint(text[at+1:], 10, 64)
	if err != nil {
		return SnapshotPublicationRef{}, errors.New("activation header epoch is invalid")
	}
	ref := SnapshotPublicationRef{SnapshotRevision: execution.SnapshotRevision(text[:at]), PublicationEpoch: epoch}
	if ref.validate() != nil {
		return SnapshotPublicationRef{}, errors.New("activation header publication is invalid")
	}
	return ref, nil
}

// rebuildActivationBodyScript writes the body back only while the header is
// the one read, no body has appeared, and every timeline the candidate was
// checked against is unchanged: still the bytes read, or still absent where
// the fence is empty. The header, the timelines and the revision are not
// touched: the body is the one the header already describes.
//
// Like the cutover script it touches keys with no common hash tag, which a
// standalone or Sentinel deployment allows and Redis Cluster refuses with
// CROSSSLOT; the configuration refuses Cluster for the same reason.
const rebuildActivationBodyScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
if redis.call('EXISTS', KEYS[2]) == 1 then return 0 end
for index = 3, #KEYS do
  local current = redis.call('GET', KEYS[index])
  if ARGV[index] == '' then
    if current then return 0 end
  elseif not current or redis.sha1hex(current) ~= ARGV[index] then
    return 0
  end
end
redis.call('SET', KEYS[2], ARGV[2])
return 1
`

// RebuildActivationBody writes back the activation body under a header that
// is present without it. The body is not recomputed or guessed: every writer
// keeps it equal to the union of the Plans of the open Segments of the current
// publication's Query Groups, so it is recovered from those - byte for byte
// from the copy this process last read under the same header when it has one,
// and from the Segments themselves when it does not. Either candidate is
// checked against the Segments before it is written.
//
// A Query Group whose timeline is gone or does not decode is not read, and
// does not keep the body from every other Query Group: it keeps its place in
// the active set (or in Draining, where the copy last read held it), its
// Plans are left to its own timeline, and it is fenced on what was found - no
// key, or the bytes that did not decode. Left out instead, it would read as
// a Query Group that left: its owner would let it go, and nothing would give
// it a timeline again before the next publication.
func (repository *RedisCatalogRepository) RebuildActivationBody(ctx context.Context) (ActivationRebuildOutcome, error) {
	if repository == nil || repository.client == nil {
		return "", errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	version, err := repository.fetchControlVersion(ctx)
	if err != nil {
		return "", err
	}
	if !version.known || version.activationLen > 0 {
		return ActivationRebuildNotNeeded, nil
	}
	revision, current, pending, err := parseActivationHeader(version.header)
	if err != nil {
		return ActivationRebuildHeaderUnparsable, fmt.Errorf("%w: %v", ErrActivationRebuildRefused, err)
	}
	if pending != nil {
		return ActivationRebuildHeaderPending, fmt.Errorf("%w: the header names a pending publication", ErrActivationRebuildRefused)
	}
	published, err := repository.loadPublishedGroups(ctx, current)
	if err != nil {
		return "", err
	}
	identities := published.identities()
	last, haveLast := repository.lastGood.under(version.header)
	keys := []string{repository.activationHeaderKey(), repository.activationKey()}
	fences := make([]interface{}, 0, len(identities))
	var plans []PlanActivationRecord
	var active []execution.QueryGroupIdentity
	var held []DrainingQueryGroup
	var unread []SkippedTimeline
	unreadPlans := make(map[execution.PlanKey]struct{})
	open := make(map[execution.QueryGroupIdentity]persistedScheduleSegment, len(identities))
	for _, identity := range identities {
		timeline, raw, err := repository.loadScheduleTimelineForUpdate(ctx, identity)
		var undecodable *DeterministicScheduleError
		if errors.Is(err, ErrScheduleUnavailable) || (errors.As(err, &undecodable) && raw != nil) {
			entry := SkippedTimeline{QueryGroup: identity, Reason: SkippedTimelineAbsent}
			fence := ""
			if raw != nil {
				entry.Reason = skippedTimelineReason(raw)
				sum := sha1.Sum(raw)
				fence = hex.EncodeToString(sum[:])
			}
			unread = append(unread, entry)
			keys = append(keys, repository.scheduleTimelineKey(identity))
			fences = append(fences, fence)
			for _, plan := range published.groups[identity].Plans {
				unreadPlans[plan.Key()] = struct{}{}
			}
			if retired, ok := heldIn(last, haveLast, identity); ok {
				held = append(held, retired)
			} else {
				active = append(active, identity)
			}
			continue
		}
		if err != nil {
			return "", err
		}
		keys = append(keys, repository.scheduleTimelineKey(identity))
		fences = append(fences, timelineFence(raw))
		last := len(timeline.Segments) - 1
		switch {
		case timeline.RetiredAt != nil:
			// In the publication and retired: held, waiting to drain before
			// it comes back.
			held = append(held, DrainingQueryGroup{QueryGroup: identity, RetiredBoundary: *timeline.RetiredAt})
		case last >= 0 && timeline.Segments[last].Schedule.Segment.End == nil:
			segment := timeline.Segments[last]
			open[identity] = segment
			plans = append(plans, segment.Plans...)
			active = append(active, identity)
		default:
			return ActivationRebuildCoverageInvalid, fmt.Errorf("%w: Query Group %s has neither an open Segment nor a retirement",
				ErrActivationRebuildRefused, identity)
		}
	}
	activeRef, _, err := repository.persistAndVerifyActiveQGSet(ctx, active)
	if err != nil {
		return "", err
	}

	var payload []byte
	outcome := ActivationRebuilt
	if haveLast {
		// The body as it was, Draining included. The Segments still decide
		// whether it may be written.
		if last.state.ActiveQGSetRef != activeRef {
			return ActivationRebuildCoverageInvalid, fmt.Errorf("%w: the last activation read names another active set",
				ErrActivationRebuildRefused)
		}
		// A head carries no records to hold against the Segments; the ones
		// this process wrote with it do, when it wrote that head.
		covered := last.state
		if full, ok := repository.written.lookup(last.payload); ok {
			covered = full
		}
		if err := checkRebuiltCoverage(covered, open, unreadPlans); err != nil {
			return ActivationRebuildCoverageInvalid, fmt.Errorf("%w: %v", ErrActivationRebuildRefused, err)
		}
		payload = []byte(last.payload)
	} else {
		sort.Slice(plans, func(i, j int) bool { return lessPlanIdentity(plans[i].Fact.Plan, plans[j].Fact.Plan) })
		sort.Slice(held, func(i, j int) bool { return held[i].QueryGroup < held[j].QueryGroup })
		state := ActivationState{SchemaVersion: activationSchemaVersion, RecordRevision: revision, Current: current,
			Plans: plans, Draining: held, ActiveQGSetRef: activeRef}
		if err := checkRebuiltCoverage(state, open, unreadPlans); err != nil {
			return ActivationRebuildCoverageInvalid, fmt.Errorf("%w: %v", ErrActivationRebuildRefused, err)
		}
		payload, err = encodeActivationHead(state)
		if err != nil {
			return "", err
		}
		outcome = ActivationRebuiltDrainingUnknown
	}
	args := append([]interface{}{version.header, payload}, fences...)
	changed, err := repository.client.Eval(ctx, rebuildActivationBodyScript, keys, args...).Int()
	if err != nil {
		return "", activationDependencyIO(fmt.Errorf("rebuild activation body: %w", err))
	}
	if changed != 1 {
		return ActivationRebuildConflict, nil
	}
	repository.clearActivationCaches()
	if len(unread) > 0 {
		// The records this process wrote with the head would answer for the
		// unread Query Groups as if their timelines were there; read from the
		// Segments instead, they are named, and Ensure repairs them.
		repository.written.forget()
		repository.rebuilds.addUnread(unread)
	}
	return outcome, nil
}

// heldIn is the Draining entry the copy last read gives a Query Group, when
// there is a copy and it holds the Query Group there.
func heldIn(last *parsedActivation, haveLast bool, identity execution.QueryGroupIdentity) (DrainingQueryGroup, bool) {
	if !haveLast {
		return DrainingQueryGroup{}, false
	}
	for _, draining := range last.state.Draining {
		if draining.QueryGroup == identity {
			return draining, true
		}
	}
	return DrainingQueryGroup{}, false
}

// checkRebuiltCoverage is the invariant every writer of the body keeps: its
// Plans are exactly the Plans of the open Segments, each equal, none twice.
// The Plans of a Query Group whose timeline was not read have no Segment to
// hold them against; the body may carry them, and nothing else unopened.
//
// A head carries no records, so all that is left of it is that no Plan is
// open in two Query Groups.
func checkRebuiltCoverage(state ActivationState, open map[execution.QueryGroupIdentity]persistedScheduleSegment,
	unread map[execution.PlanKey]struct{}) error {
	head := state.SchemaVersion == activationHeadSchemaVersion
	expected, err := activationRecordMap(state.Plans)
	if err != nil {
		return err
	}
	covered := make(map[execution.PlanKey]struct{}, len(expected))
	for _, segment := range open {
		if !head {
			if err := validateOpenSegmentActivation(state, segment); err != nil {
				return err
			}
		}
		for _, record := range segment.Plans {
			if _, duplicate := covered[record.Fact.Key()]; duplicate {
				return errors.New("a Plan is carried by two open Segments")
			}
			covered[record.Fact.Key()] = struct{}{}
		}
	}
	if !head {
		for key := range expected {
			_, carried := covered[key]
			if _, unknown := unread[key]; !carried && !unknown {
				return errors.New("the body holds a Plan no open Segment carries")
			}
		}
	}
	return nil
}
