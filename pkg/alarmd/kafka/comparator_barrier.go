// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/comparator"
)

type comparatorBarrierAdapter struct {
	records      *comparatorRecordCoordinator
	beginCapture func(string) (time.Time, error)
	lastCoverage map[string]string
}

func newComparatorBarrierAdapter(records *comparatorRecordCoordinator) (*comparatorBarrierAdapter, error) {
	if records == nil || records.assignment == nil || records.assignment.metadata == nil || records.run == nil {
		return nil, errors.New("kafka comparator barrier: initialized record coordinator is required")
	}
	return &comparatorBarrierAdapter{
		records: records, beginCapture: records.run.BeginBarrierCapture, lastCoverage: make(map[string]string),
	}, nil
}

// CaptureOverdue freezes fresh broker high-water barriers for every overdue
// coverage entry that has not already frozen one.
func (a *comparatorBarrierAdapter) CaptureOverdue(ctx context.Context) (int, error) {
	if a == nil || a.records == nil || ctx == nil || a.beginCapture == nil {
		return 0, errors.New("kafka comparator barrier: initialized adapter and context are required")
	}
	records := a.records
	records.mu.Lock()
	defer records.mu.Unlock()
	if records.failed != nil {
		return 0, records.failed
	}
	if err := barrierContextError(ctx, records.session); err != nil {
		return 0, records.fail(err)
	}
	assignments, err := records.acquireBarrierOperation()
	if err != nil {
		return 0, records.fail(err)
	}
	defer records.releaseOperation()

	snapshots, err := records.run.SweepCoverage(records.epoch)
	if err != nil {
		return 0, records.fail(err)
	}
	a.pruneLastCoverage(snapshots)
	candidates := make([]comparator.CoverageSnapshot, 0, len(snapshots))
	missingRoles := make(map[comparator.StreamRole]struct{}, 2)
	for _, snapshot := range snapshots {
		if snapshot.Phase != comparator.CoverageOverdue || snapshot.BarrierFrozen {
			continue
		}
		candidates = append(candidates, snapshot)
		for _, role := range snapshot.MissingRoles {
			missingRoles[role] = struct{}{}
		}
	}
	if len(candidates) == 0 {
		if err := a.publishChangedCoverage(ctx, snapshots); err != nil {
			return 0, records.fail(err)
		}
		return 0, nil
	}
	sort.Slice(assignments, func(left, right int) bool {
		if assignments[left].Role != assignments[right].Role {
			return assignments[left].Role < assignments[right].Role
		}
		if assignments[left].Topic != assignments[right].Topic {
			return assignments[left].Topic < assignments[right].Topic
		}
		return assignments[left].Partition < assignments[right].Partition
	})
	targets := make([]comparator.PartitionAssignment, 0, len(assignments))
	for _, assignment := range assignments {
		if _, ok := missingRoles[assignment.Role]; ok {
			targets = append(targets, assignment)
		}
	}
	if len(targets) == 0 {
		return 0, records.fail(errors.New("kafka comparator barrier: missing roles have no assigned partitions"))
	}
	capturedAt, err := a.beginCapture(records.epoch)
	if err != nil {
		return 0, records.fail(err)
	}
	highWater := make(map[barrierCoordinate]int64, len(targets))
	for _, target := range targets {
		if err := barrierContextError(ctx, records.session); err != nil {
			return 0, records.fail(err)
		}
		offset, err := records.assignment.metadata.GetOffset(target.Topic, target.Partition, sarama.OffsetNewest)
		if err != nil {
			return 0, records.fail(fmt.Errorf("kafka comparator barrier: read high water: %w", err))
		}
		if offset < 0 || offset == math.MaxInt64 {
			return 0, records.fail(errors.New("kafka comparator barrier: high water is out of range"))
		}
		highWater[barrierCoordinate{role: target.Role, topic: target.Topic, partition: target.Partition}] = offset
		if err := barrierContextError(ctx, records.session); err != nil {
			return 0, records.fail(err)
		}
	}

	records.assignment.mu.Lock()
	generation, activeErr := records.assignment.currentGenerationLocked(
		records.handle,
		sessionAssignmentID(records.session),
	)
	if activeErr == nil && (!generation.active || generation.run != records.run || generation.epoch != records.epoch) {
		activeErr = generationError(generation)
	}
	if activeErr == nil {
		for _, candidate := range candidates {
			barriers := make([]comparator.PartitionBarrier, 0, len(targets))
			candidateRoles := make(map[comparator.StreamRole]struct{}, len(candidate.MissingRoles))
			for _, role := range candidate.MissingRoles {
				candidateRoles[role] = struct{}{}
			}
			for _, target := range targets {
				if _, ok := candidateRoles[target.Role]; !ok {
					continue
				}
				barriers = append(barriers, comparator.PartitionBarrier{
					Role: target.Role, Topic: target.Topic, Partition: target.Partition,
					HighWater: highWater[barrierCoordinate{role: target.Role, topic: target.Topic, partition: target.Partition}],
				})
			}
			if err := records.run.FreezeBarrier(records.epoch, candidate.InputID, comparator.BarrierSnapshot{
				CaptureStartedAt: capturedAt,
				Partitions:       barriers,
			}); err != nil {
				activeErr = err
				break
			}
		}
	}
	records.assignment.mu.Unlock()
	if activeErr != nil {
		return 0, records.fail(activeErr)
	}
	refreshed, err := records.run.SweepCoverage(records.epoch)
	if err != nil {
		return 0, records.fail(err)
	}
	if err := a.publishChangedCoverage(ctx, refreshed); err != nil {
		return 0, records.fail(err)
	}
	return len(candidates), nil
}

func (a *comparatorBarrierAdapter) publishChangedCoverage(ctx context.Context, snapshots []comparator.CoverageSnapshot) error {
	authoritativeIDs := make([]string, 0)
	terminalIDs := make([]string, 0)
	terminalSnapshots := make([]comparator.CoverageSnapshot, 0)
	signatures := make(map[string]string)
	for _, snapshot := range snapshots {
		if !snapshot.BarrierFrozen {
			continue
		}
		signature := coverageAuditSignature(snapshot)
		if a.lastCoverage[snapshot.InputID] == signature {
			continue
		}
		if snapshot.Authoritative && snapshot.Phase == comparator.CoverageMissingAtBarrier {
			authoritativeIDs = append(authoritativeIDs, snapshot.InputID)
		}
		if snapshot.Phase == comparator.CoverageMissingAtBarrier {
			terminalIDs = append(terminalIDs, snapshot.InputID)
			terminalSnapshots = append(terminalSnapshots, snapshot)
		}
		signatures[snapshot.InputID] = signature
	}
	if len(signatures) == 0 {
		return nil
	}
	if len(authoritativeIDs) > 0 {
		batches, err := a.records.run.AuditBatches(a.records.epoch, authoritativeIDs, comparator.Gates{StableEpoch: true})
		if err != nil {
			return err
		}
		for _, batch := range batches {
			if err := a.records.audits.WriteBatch(ctx, batch); err != nil {
				return fmt.Errorf("kafka comparator barrier: publish audit: %w", err)
			}
		}
	}
	if len(terminalIDs) > 0 {
		if err := a.records.run.ReleaseTerminalCoverage(a.records.epoch, terminalIDs); err != nil {
			return err
		}
		a.records.diagnostics.coverageRelease(summarizeCoverageRelease(terminalSnapshots))
	}
	for inputID, signature := range signatures {
		a.lastCoverage[inputID] = signature
	}
	return nil
}

func summarizeCoverageRelease(snapshots []comparator.CoverageSnapshot) ComparatorCoverageRelease {
	event := ComparatorCoverageRelease{Entries: len(snapshots)}
	for _, snapshot := range snapshots {
		if snapshot.Authoritative {
			event.Authoritative++
		} else {
			event.Orphans++
		}
		for _, role := range snapshot.MissingAtBarrierRoles {
			switch role {
			case comparator.StreamInput:
				event.MissingInput++
			case comparator.StreamGo:
				event.MissingGo++
			case comparator.StreamPython:
				event.MissingPython++
			}
		}
	}
	return event
}

func (a *comparatorBarrierAdapter) pruneLastCoverage(snapshots []comparator.CoverageSnapshot) {
	current := make(map[string]struct{}, len(snapshots))
	for _, snapshot := range snapshots {
		current[snapshot.InputID] = struct{}{}
	}
	for inputID := range a.lastCoverage {
		if _, ok := current[inputID]; !ok {
			delete(a.lastCoverage, inputID)
		}
	}
}

func coverageAuditSignature(snapshot comparator.CoverageSnapshot) string {
	return fmt.Sprintf("%t|%d|%t|%v|%v|%v", snapshot.Authoritative, snapshot.Phase, snapshot.BarrierFrozen, snapshot.MissingRoles, snapshot.MissingAtBarrierRoles, snapshot.LateRoles)
}

type barrierCoordinate struct {
	role      comparator.StreamRole
	topic     string
	partition int32
}

func barrierContextError(ctx context.Context, session sarama.ConsumerGroupSession) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if session == nil || session.Context() == nil {
		return errors.New("kafka comparator barrier: session context is required")
	}
	return session.Context().Err()
}
