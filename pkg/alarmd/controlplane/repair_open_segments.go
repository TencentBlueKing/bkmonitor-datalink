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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// repairOpenSegmentScript rewrites one schedule timeline if, and only if, the
// bytes it is replacing are still the bytes that were read.
//
// The same fence the cutover uses, for the same reason: another writer moving
// the timeline between the read and the write must lose, and it must lose
// visibly rather than by having its work overwritten.
const repairOpenSegmentScript = `
local current = redis.call('GET', KEYS[1])
if not current then return 0 end
if redis.sha1hex(current) ~= ARGV[2] then return 0 end
redis.call('SET', KEYS[1], ARGV[1])
return 1
`

// OpenSegmentRepair is one Query Group's open Segment measured against what the
// current activation's publication names it.
type OpenSegmentRepair struct {
	QueryGroup     execution.QueryGroupIdentity
	OpenDigest     execution.ObjectDigest
	ManifestDigest execution.ObjectDigest
	RefsMatch      bool
	RecordRevision uint64
	// Applied is set when this repair was written. Always false on a dry run.
	Applied bool
}

// Mismatched reports whether this Segment names something other than what the
// publication names.
func (r OpenSegmentRepair) Mismatched() bool {
	return r.OpenDigest != r.ManifestDigest || !r.RefsMatch
}

// OpenSegmentRepairReport is what one run found and, if it was applied, did.
type OpenSegmentRepairReport struct {
	Revision  execution.SnapshotRevision
	Scanned   int
	Repairs   []OpenSegmentRepair
	Applied   int
	Conflicts int
}

// RepairOpenSegmentsOptions configures one run.
type RepairOpenSegmentsOptions struct {
	// Apply writes. Absent, the run only reports what it would write, which is
	// the default because this is a one-off operator action on live execution
	// state and the first thing anyone wants is to see the list.
	Apply bool
	// EvidenceDir receives the stored bytes of every key this run changes,
	// before and after. Required when applying: a repair nobody can read back
	// afterwards is a change nobody can check or undo.
	EvidenceDir string
}

// RepairOpenSegments puts open Segments back on the object their publication
// names.
//
// It exists because a Segment could once be cut from a group that assembly had
// quietly changed, so its name and the manifest's disagreed permanently and
// every later cutover refused. The cutter copies the manifest's names now and
// verifies the content against them, so no new Segment can end up this way --
// but the ones already stored still do, and the cutover that would replace
// them is the very thing they block.
//
// Deliberately a one-off operator command and not a self-healing branch. A
// self-heal would have to stay in the code for a state that can no longer be
// produced, and would quietly repair the next defect of this shape instead of
// letting it be seen.
//
// It rewrites the open Segment's names in place and touches nothing else. Not
// the activation: changing StateApplyEpoch would put the whole fleet back into
// warmup. Not the timeline key: deleting one is, to a Query Group the current
// activation runs, the same as a Segment that never existed. Not the object
// store: the objects are content-addressed and shared. The names are the only
// thing wrong and the only thing changed.
func RepairOpenSegments(
	ctx context.Context,
	repository *RedisCatalogRepository,
	options RepairOpenSegmentsOptions,
) (OpenSegmentRepairReport, error) {
	if repository == nil || repository.client == nil {
		return OpenSegmentRepairReport{}, errors.New("alarmd controlplane: repair requires a catalog repository")
	}
	if options.Apply && options.EvidenceDir == "" {
		return OpenSegmentRepairReport{}, errors.New(
			"alarmd controlplane: applying a repair requires an evidence directory")
	}
	activation, err := repository.LoadActivation(ctx)
	if err != nil {
		return OpenSegmentRepairReport{}, err
	}
	revision := activation.Current.SnapshotRevision
	if revision == "" {
		return OpenSegmentRepairReport{}, errors.New(
			"alarmd controlplane: the activation names no publication to repair against")
	}
	manifest, err := repository.LoadCatalogManifest(ctx, revision)
	if err != nil {
		return OpenSegmentRepairReport{}, err
	}
	named := make(map[execution.QueryGroupIdentity]ManifestQueryGroup, len(manifest.QueryGroups))
	for _, entry := range manifest.QueryGroups {
		named[entry.QueryGroup] = entry
	}
	refsByGroup, err := repository.manifestRefsByGroup(ctx, activation.Current)
	if err != nil {
		return OpenSegmentRepairReport{}, err
	}

	report := OpenSegmentRepairReport{Revision: revision}
	groups, err := repository.scanScheduleTimelines(ctx)
	if err != nil {
		return OpenSegmentRepairReport{}, err
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	for _, group := range groups {
		report.Scanned++
		timeline, raw, err := repository.loadScheduleTimelineForUpdate(ctx, group)
		if err != nil {
			return report, fmt.Errorf("read %s: %w", group, err)
		}
		if timeline.RetiredAt != nil || len(timeline.Segments) == 0 {
			continue
		}
		open := timeline.Segments[len(timeline.Segments)-1].Schedule.Segment
		if open.End != nil {
			continue
		}
		entry, ok := named[group]
		if !ok {
			// The publication does not name this Query Group at all. That is a
			// different fact from a Segment on the wrong object, and repairing
			// it would mean inventing a name -- which is the thing that caused
			// this.
			continue
		}
		repair := OpenSegmentRepair{
			QueryGroup: group, OpenDigest: open.ObjectDigest, ManifestDigest: entry.ObjectDigest,
			RefsMatch:      execution.SameOutputContextRefs(open.OutputContextRefs, refsByGroup[group]),
			RecordRevision: timeline.RecordRevision,
		}
		if !repair.Mismatched() {
			continue
		}
		if !options.Apply {
			report.Repairs = append(report.Repairs, repair)
			continue
		}
		if err := writeRepairEvidence(options.EvidenceDir, group, "before", raw); err != nil {
			return report, err
		}
		timeline.Segments[len(timeline.Segments)-1].Schedule.Segment.ObjectDigest = entry.ObjectDigest
		timeline.Segments[len(timeline.Segments)-1].Schedule.Segment.OutputContextRefs = refsByGroup[group]
		timeline.RecordRevision++
		payload, err := json.Marshal(timeline)
		if err != nil {
			return report, err
		}
		changed, err := repository.client.Eval(ctx, repairOpenSegmentScript,
			[]string{repository.scheduleTimelineKey(group)}, payload, timelineFence(raw)).Int()
		if err != nil {
			return report, fmt.Errorf("write %s: %w", group, err)
		}
		if changed != 1 {
			// Somebody moved it between the read and the write. Reported, not
			// retried: a repair racing a writer it cannot see should stop and
			// be run again, not push.
			report.Conflicts++
			report.Repairs = append(report.Repairs, repair)
			continue
		}
		after, err := repository.client.Get(ctx, repository.scheduleTimelineKey(group)).Bytes()
		if err != nil {
			return report, err
		}
		if err := writeRepairEvidence(options.EvidenceDir, group, "after", after); err != nil {
			return report, err
		}
		repair.Applied = true
		report.Applied++
		report.Repairs = append(report.Repairs, repair)
	}
	return report, nil
}

// manifestRefsByGroup collects the output context references the manifest names
// for each Query Group, in the order a Segment carries them.
func (repository *RedisCatalogRepository) manifestRefsByGroup(
	ctx context.Context, publication SnapshotPublicationRef,
) (map[execution.QueryGroupIdentity][]execution.OutputContextRef, error) {
	content, err := repository.LoadPublishedContent(ctx, publication)
	if err != nil {
		return nil, err
	}
	refs := make(map[execution.QueryGroupIdentity][]execution.OutputContextRef, len(content.Groups))
	for identity, entry := range content.Groups {
		refs[identity] = entry.Refs
	}
	return refs, nil
}

// scanScheduleTimelines lists the Query Groups that have a schedule timeline.
func (repository *RedisCatalogRepository) scanScheduleTimelines(
	ctx context.Context,
) ([]execution.QueryGroupIdentity, error) {
	prefix := repository.scheduleTimelineKey("")
	var groups []execution.QueryGroupIdentity
	var cursor uint64
	for {
		keys, next, err := repository.client.Scan(ctx, cursor, prefix+"*", 512).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			if len(key) <= len(prefix) {
				continue
			}
			groups = append(groups, execution.QueryGroupIdentity(key[len(prefix):]))
		}
		if next == 0 {
			return groups, nil
		}
		cursor = next
	}
}

func writeRepairEvidence(directory string, group execution.QueryGroupIdentity, stage string, payload []byte) error {
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, fmt.Sprintf("%s.%s.json", group, stage)), payload, 0o640)
}

// RepairFenceForTest runs the repair's compare-and-set with the given bytes and
// the fence of the bytes it claims to be replacing, and reports whether it
// wrote. It exists so the fence can be exercised where the Redis fixture is.
func RepairFenceForTest(
	ctx context.Context, repository *RedisCatalogRepository, key string, payload, expected []byte,
) (bool, error) {
	changed, err := repository.client.Eval(ctx, repairOpenSegmentScript,
		[]string{key}, payload, timelineFence(expected)).Int()
	return changed == 1, err
}
