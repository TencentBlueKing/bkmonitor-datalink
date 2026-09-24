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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A publication cutover used to fail whole when one Query Group's open
// Segment failed a precondition that only a write outside the cutover could
// have broken: the fleet stayed on the previous activation and every later
// publication failed at the same place, so no new strategy and no edit took
// effect until someone ran the repair subcommand. Now that Query Group alone
// is held back - blocked - and the rest of the publication is activated.
//
// A blocked Query Group keeps the activation records it had and its timeline
// is not written. It is read and judged again at every cutover until its
// precondition holds, which is what the persisted set below is for: once the
// manifest names the new content, nothing else would say this Query Group is
// not running it, and the next cutover would take it for unchanged and never
// look again.
const (
	blockedSchemaVersion = "alarmd-control-activation-blocked-v1"
	// blockedDetailMax bounds the free-text detail of one entry. With the
	// set capped at the publication's Query Group count, the key is at most
	// that count times one entry: the identifiers, two digests and their
	// context refs, and this.
	blockedDetailMax = 256
)

// BlockedQueryGroup is one Query Group a cutover held back.
//
// The two digest pairs answer different questions and must not be merged.
// Activated is the content the carried records were activated with: the next
// cutover judges its preconditions against it, as it would have against the
// manifest before the block. Open is what the timeline's open Segment names,
// which is what a Worker executes: the view, the content scope and the object
// prefetch follow it. Open is empty when the timeline has no Segment that can
// execute - no Segment, retired, closed or starting past the boundary,
// unreadable - and then the Query Group is left out of the view.
type BlockedQueryGroup struct {
	QueryGroup execution.QueryGroupIdentity `json:"query_group"`
	Reason     string                       `json:"reason"`
	Detail     string                       `json:"detail,omitempty"`
	Since      execution.EvaluationTime     `json:"since"`
	// Plans are the Plans whose records the Query Group carries; the
	// manifest cannot say, since it names what the Query Group was meant to
	// run next.
	Plans           []execution.PlanKey          `json:"plans,omitempty"`
	ActivatedDigest execution.ObjectDigest       `json:"activated_digest,omitempty"`
	ActivatedRefs   []execution.OutputContextRef `json:"activated_refs,omitempty"`
	OpenDigest      execution.ObjectDigest       `json:"open_object_digest,omitempty"`
	OpenRefs        []execution.OutputContextRef `json:"open_output_context_refs,omitempty"`
}

type blockedSetPayload struct {
	SchemaVersion string              `json:"schema_version"`
	QueryGroups   []BlockedQueryGroup `json:"query_groups"`
}

// ErrBlockedSetUnreadable is the persisted blocked set existing and not
// decoding. It is not recoverable by reading again; the cutover reads every
// timeline instead, and the next write replaces it.
var ErrBlockedSetUnreadable = errors.New("alarmd controlplane: blocked Query Group set is unreadable")

func (repository *RedisCatalogRepository) activationBlockedKey() string {
	return repository.prefix + ":activation_blocked"
}

// loadBlockedSet reads the persisted set, sorted by Query Group. Absent is an
// empty set.
func (repository *RedisCatalogRepository) loadBlockedSet(ctx context.Context) ([]BlockedQueryGroup, error) {
	payload, err := repository.client.Get(ctx, repository.activationBlockedKey()).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, activationDependencyIO(err)
	}
	var set blockedSetPayload
	if err := json.Unmarshal(payload, &set); err != nil || set.SchemaVersion != blockedSchemaVersion {
		return nil, ErrBlockedSetUnreadable
	}
	sort.Slice(set.QueryGroups, func(i, j int) bool { return set.QueryGroups[i].QueryGroup < set.QueryGroups[j].QueryGroup })
	return set.QueryGroups, nil
}

// encodeBlockedSet is the persisted form, or empty for an empty set: the
// cutover script deletes the key then.
func encodeBlockedSet(groups []BlockedQueryGroup) ([]byte, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	sorted := append([]BlockedQueryGroup(nil), groups...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].QueryGroup < sorted[j].QueryGroup })
	return json.Marshal(blockedSetPayload{SchemaVersion: blockedSchemaVersion, QueryGroups: sorted})
}

// blockedDigest names a set for the activation body, which carries it with
// the count so a set that went missing, or one written beside an activation
// that does not account for it, is noticed rather than trusted.
func blockedDigest(groups []BlockedQueryGroup) string {
	if len(groups) == 0 {
		return ""
	}
	identities := make([]string, len(groups))
	for index, group := range groups {
		identities[index] = string(group.QueryGroup) + "\x00" + group.Reason
	}
	sort.Strings(identities)
	sum := sha256.New()
	for _, identity := range identities {
		sum.Write([]byte(identity))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// BlockedSetAccounting is how the persisted set compares with what the
// activation body says it holds.
type BlockedSetAccounting string

const (
	BlockedSetConsistent BlockedSetAccounting = "consistent"
	// BlockedSetLost: the body counts entries the key does not hold - the key
	// was evicted, or lost in a failover.
	BlockedSetLost BlockedSetAccounting = "lost"
	// BlockedSetUnaccounted: the key holds entries the body does not count -
	// written by a leader that does not know the set, or a body rebuilt
	// without it.
	BlockedSetUnaccounted BlockedSetAccounting = "unaccounted"
	// BlockedSetUnreadable: the key exists and does not decode.
	BlockedSetUnreadable BlockedSetAccounting = "unreadable"
)

// BlockedSetAccountings is the closed list, for the metric.
var BlockedSetAccountings = []BlockedSetAccounting{BlockedSetConsistent, BlockedSetLost, BlockedSetUnaccounted, BlockedSetUnreadable}

// loadAccountedBlockedSet reads the set and checks it against the body. A
// set that does not agree with the body is still returned where it could be
// read - its entries are still Query Groups to look at - but the caller must
// not rely on it being all of them.
func (repository *RedisCatalogRepository) loadAccountedBlockedSet(
	ctx context.Context, activation ActivationState,
) ([]BlockedQueryGroup, BlockedSetAccounting, error) {
	groups, err := repository.loadBlockedSet(ctx)
	if errors.Is(err, ErrBlockedSetUnreadable) {
		return nil, BlockedSetUnreadable, nil
	}
	if err != nil {
		return nil, "", err
	}
	switch {
	case activation.BlockedCount == len(groups) && activation.BlockedDigest == blockedDigest(groups):
		return groups, BlockedSetConsistent, nil
	case len(groups) == 0:
		return groups, BlockedSetLost, nil
	case activation.BlockedCount == 0:
		return groups, BlockedSetUnaccounted, nil
	default:
		return groups, BlockedSetLost, nil
	}
}

// ActivationBlocked is the blocked set as the readers of the running content
// see it: the view, the content scopes and the object renewal. An
// unreadable set reads as empty here - those readers then fall back to the
// manifest, which is what they did before the set existed.
func (repository *RedisCatalogRepository) ActivationBlocked(ctx context.Context) ([]BlockedQueryGroup, error) {
	if repository == nil || repository.client == nil {
		return nil, errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	groups, err := repository.loadBlockedSet(ctx)
	if errors.Is(err, ErrBlockedSetUnreadable) {
		return nil, nil
	}
	return groups, err
}

// blockedCounts is the leader's last reading of the set, for the metric.
type blockedCounts struct {
	mu         sync.Mutex
	byReason   map[string]int
	samples    []string
	accounting map[BlockedSetAccounting]uint64
	reopened   uint64
}

// blockedSampleMax bounds the Query Groups named beside the counts.
const blockedSampleMax = 8

func (counts *blockedCounts) record(groups []BlockedQueryGroup, accounting BlockedSetAccounting, reopened int) {
	counts.mu.Lock()
	defer counts.mu.Unlock()
	counts.byReason = make(map[string]int)
	counts.samples = counts.samples[:0]
	for _, group := range groups {
		counts.byReason[group.Reason]++
		if len(counts.samples) < blockedSampleMax {
			counts.samples = append(counts.samples, string(group.QueryGroup)+":"+group.Reason)
		}
	}
	if counts.accounting == nil {
		counts.accounting = make(map[BlockedSetAccounting]uint64)
	}
	if accounting != "" {
		counts.accounting[accounting]++
	}
	counts.reopened += uint64(reopened)
}

// ActivationBlockedReading is the leader's last cutover as far as held-back
// Query Groups go.
type ActivationBlockedReading struct {
	// ByReason is the Query Groups held back at the last cutover, by reason;
	// Samples names up to eight of them as query_group:reason.
	ByReason map[string]int
	Samples  []string
	// Accounting counts cutovers by how the persisted set compared with the
	// activation body; Reopened counts timelines opened again because their
	// key was gone.
	Accounting map[BlockedSetAccounting]uint64
	Reopened   uint64
}

// ActivationBlockedReading is what the metric and the fleet page read.
func (repository *RedisCatalogRepository) ActivationBlockedReading() ActivationBlockedReading {
	byReason, accounting, reopened := repository.ActivationBlockedCounts()
	reading := ActivationBlockedReading{ByReason: byReason, Accounting: accounting, Reopened: reopened}
	if repository != nil {
		repository.blocked.mu.Lock()
		reading.Samples = append([]string(nil), repository.blocked.samples...)
		repository.blocked.mu.Unlock()
	}
	return reading
}

// ActivationBlockedCounts is the counts of ActivationBlockedReading.
func (repository *RedisCatalogRepository) ActivationBlockedCounts() (map[string]int, map[BlockedSetAccounting]uint64, uint64) {
	if repository == nil {
		return nil, nil, 0
	}
	counts := &repository.blocked
	counts.mu.Lock()
	defer counts.mu.Unlock()
	byReason := make(map[string]int, len(counts.byReason))
	for reason, count := range counts.byReason {
		byReason[reason] = count
	}
	accounting := make(map[BlockedSetAccounting]uint64, len(counts.accounting))
	for key, count := range counts.accounting {
		accounting[key] = count
	}
	return byReason, accounting, counts.reopened
}

// blockedSetUnchanged tells the cutover script to leave the set as it is:
// an activation that keeps its publication keeps the Query Groups a cutover
// held back from it.
var blockedSetUnchanged = []byte("=")

func truncateBlockedDetail(detail string) string {
	if len(detail) <= blockedDetailMax {
		return detail
	}
	return detail[:blockedDetailMax]
}

// blockedAt is one Query Group this cutover holds back, with the since of an
// earlier block of the same Query Group kept.
func blockedAt(
	previous map[execution.QueryGroupIdentity]BlockedQueryGroup,
	group execution.QueryGroupIdentity, reason, detail string, boundary execution.EvaluationTime,
	plans []execution.PlanKey, activatedDigest execution.ObjectDigest, activatedRefs []execution.OutputContextRef,
	openDigest execution.ObjectDigest, openRefs []execution.OutputContextRef,
) BlockedQueryGroup {
	since := boundary
	if earlier, ok := previous[group]; ok && earlier.Since > 0 && earlier.Since < since {
		since = earlier.Since
	}
	return BlockedQueryGroup{QueryGroup: group, Reason: reason, Detail: truncateBlockedDetail(detail), Since: since,
		Plans: append([]execution.PlanKey(nil), plans...), ActivatedDigest: activatedDigest, ActivatedRefs: activatedRefs, OpenDigest: openDigest, OpenRefs: openRefs}
}

// applyBlockedToActivatedContent makes the content the activation runs read
// true for blocked Query Groups: they run what their carried records were
// activated with, not what the manifest names. Both the reconciler and the
// cutover read the activated content through here, so the two decide a
// blocked Query Group the same way.
func applyBlockedToActivatedContent(content *activatedContent, groups []BlockedQueryGroup) {
	for _, group := range groups {
		if _, present := content.groups[group.QueryGroup]; !present {
			continue
		}
		if group.ActivatedDigest == "" {
			// Nothing activated to compare against: the Query Group is read.
			delete(content.digests, group.QueryGroup)
			continue
		}
		content.digests[group.QueryGroup] = group.ActivatedDigest
		for _, ref := range group.ActivatedRefs {
			content.contexts[ref.Plan] = ref.Digest
		}
	}
}

// blockedObjectKeys are the object and context keys the held-back Query
// Groups need: both the content their records were activated with and the
// content their open Segment names. Unreadable reads as none, which is what
// renewal did before the set existed.
func (repository *RedisCatalogRepository) blockedObjectKeys(ctx context.Context) []string {
	groups, err := repository.ActivationBlocked(ctx)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	keys := make([]string, 0)
	add := func(key string) {
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for _, group := range groups {
		for _, digest := range []execution.ObjectDigest{group.ActivatedDigest, group.OpenDigest} {
			if digest != "" {
				add(repository.queryGroupObjectKey(digest))
			}
		}
		for _, refs := range [][]execution.OutputContextRef{group.ActivatedRefs, group.OpenRefs} {
			for _, ref := range refs {
				add(repository.outputContextKey(ref.Digest))
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// ApplyBlockedToContent is a publication's content made true for the Query
// Groups a cutover held back: each runs what its open Segment names, and one
// with no Segment that can run is not in the content at all. The view, the
// content scopes and the object prefetch all read the content through here,
// so the three agree. The map given is not changed: it may be the content
// the repository remembers for every caller.
func ApplyBlockedToContent(
	content map[execution.QueryGroupIdentity]ContentEntry, groups []BlockedQueryGroup,
) map[execution.QueryGroupIdentity]ContentEntry {
	if len(groups) == 0 {
		return content
	}
	result := make(map[execution.QueryGroupIdentity]ContentEntry, len(content))
	for identity, entry := range content {
		result[identity] = entry
	}
	for _, group := range groups {
		entry, present := result[group.QueryGroup]
		if !present {
			continue
		}
		if group.OpenDigest == "" {
			delete(result, group.QueryGroup)
			continue
		}
		entry.Digest = group.OpenDigest
		entry.Refs = append([]execution.OutputContextRef(nil), group.OpenRefs...)
		result[group.QueryGroup] = entry
	}
	return result
}
