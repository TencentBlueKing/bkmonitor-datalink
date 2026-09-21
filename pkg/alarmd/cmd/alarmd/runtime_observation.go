// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

func observationRedisOptions(connection config.RedisConnectionConfig) *redis.UniversalOptions {
	o := productionRedisOptions(phaseTwoDiagnosticsConnection(connection))
	// A diagnostic command gets one attempt; the next maintenance tick is its
	// retry. Transport limits also bound clients which ignore context deadlines.
	o.MaxRetries = -1
	o.ReadTimeout = min(o.ReadTimeout, time.Second)
	o.WriteTimeout = min(o.WriteTimeout, time.Second)
	o.PoolTimeout = time.Second
	return o
}

func observationSampleLimits(capacity config.ObservationCapacity) (observability.SeriesSampleLimits, bool) {
	queue := min(capacity.SampleBufferBytes/observability.SeriesSampleBufferBytes(), capacity.SampleRecordsPerMinute)
	limits := observability.SeriesSampleLimits{RecordsPerMinute: capacity.SampleRecordsPerMinute, BytesPerMinute: capacity.SampleBytesPerMinute, QueueCapacity: queue}
	return limits, queue > 0 && limits.BytesPerMinute >= observability.SeriesSampleMaxBytes
}

func observationCostOptions(capacity config.ObservationCapacity, process string, now func() time.Time) observability.CostSummaryOptions {
	// The other half pays for bounded cross-replica projection I/O and decode.
	collectorBytes := capacity.CostBytes / 2
	groups := collectorBytes / 2048
	o := observability.CostSummaryOptions{ProcessID: process, Window: 5 * time.Minute, Now: now}
	for groups > 0 {
		o.GroupCapacity = groups
		o.PlanCapacity = groups * 2
		o.MetadataBytes = collectorBytes / 8
		o.TopN = min(20, groups, max(1, (capacity.CostBytes/16)/(12*4096)))
		if observability.CostSummaryCapacityBytes(o) <= int64(collectorBytes) {
			return o
		}
		groups /= 2
	}
	return observability.CostSummaryOptions{ProcessID: process, Now: now}
}

func observationProjectionLimits(capacity config.ObservationCapacity, interval time.Duration) fleet.CostProjectionLimits {
	return fleet.CostProjectionLimits{PublishBytes: capacity.CostBytes / 16, ReadBytes: capacity.CostBytes / 16, ReadCommands: capacity.DirectoryCommands / 2, Timeout: time.Second, FreshFor: 3 * interval, TTL: 4 * interval}
}

// The existing maintenance loop publishes small cost projections. Cross-replica
// reads use only these projections and a bounded, read-only registration page;
// neither the full fleet view nor an execution Redis connection is involved.
type observationCostRefresh struct {
	store          *fleet.CostProjectionStore
	registry       *ownership.RedisStore
	reader         redis.Cmdable
	cache          *fleet.CostCandidatesCache
	limits         fleet.CostProjectionLimits
	registryLimits ownership.ObservationRegistryLimits
	replica        string
	interval       time.Duration
	last           time.Time
	offset         int64
	pageVisited    int
	observation    fleet.CostCandidatesSnapshot
}

func (r *observationCostRefresh) publish(ctx context.Context, at time.Time, cost observability.CostSnapshot) {
	if r == nil {
		return
	}
	published, publishErr := r.store.Publish(ctx, r.replica, at, cost)
	if r.last.IsZero() || at.Sub(r.last) >= r.interval {
		r.last = at
		registry := r.registry.ReadObservationRegistry(ctx, r.reader, at, r.offset, r.registryLimits)
		view := r.store.Load(ctx, registry.ReadyIDs, registry.Complete, at)
		// Both reads can paginate. Keep the registry page until the cost reader
		// has attempted each member, otherwise the two rotating cursors can
		// permanently skip alternating replicas. Re-read registrations on every
		// tick; this cursor never extends a registration's validity.
		if r.observation.Registry.Offset != registry.Offset || !slices.Equal(r.observation.Registry.ReadyIDs, registry.ReadyIDs) {
			r.pageVisited = 0
		}
		r.pageVisited += view.Attempted
		if r.pageVisited >= len(registry.ReadyIDs) {
			r.offset = registry.NextOffset
			r.pageVisited = 0
		}
		r.observation = fleet.CostCandidatesSnapshot{ObservedAt: at, Projection: view, Registry: registry, Limits: r.limits}
	}
	out := r.observation
	out.LocalPublicationFailed = publishErr != nil
	out.LocalPublishedBytes = published.WrittenBytes
	if publishErr != nil {
		out.Projection.Complete = false
		out.Projection.Gaps = append(append([]fleet.CostProjectionGap(nil), out.Projection.Gaps...), fleet.CostProjectionGap{Replica: r.replica, Reason: "LOCAL_PUBLICATION_UNAVAILABLE"})
	}
	r.cache.Update(out)
}

// Directory refresh runs on the fleet publisher's independent maintenance
// loop, not the scheduler/control loop or an HTTP caller. Only identity
// metadata enters the scalar collector; no frozen config is retained twice.
type observationRefresh struct {
	directory *controlplane.ObservationDirectory
	cost      *observability.CostSummary
	now       func() time.Time
	interval  time.Duration
	last      time.Time
	entries   int
	owned     func() []execution.QueryGroupIdentity
}

func (r *observationRefresh) publish(ctx context.Context) {
	at := r.now()
	if r.directory != nil && (r.last.IsZero() || at.Sub(r.last) >= r.interval) {
		r.last = at
		r.directory.Refresh(ctx, at)
		snapshot := r.directory.Page(at, "", "", "", 0, r.entries)
		var owned []execution.QueryGroupIdentity
		if r.owned != nil {
			owned = r.owned()
		}
		groups, complete := observationCostGroups(snapshot, owned)
		r.cost.Reconcile(groups, complete)
	}
	r.cost.Publish(at)
}

func observationCostGroups(snapshot controlplane.StrategyDirectorySnapshot, ownedGroups []execution.QueryGroupIdentity) ([]observability.CostGroup, bool) {
	byGroup := map[string]*observability.CostGroup{}
	owned := map[execution.QueryGroupIdentity]bool{}
	for _, qg := range ownedGroups {
		owned[qg] = true
	}
	for _, row := range snapshot.Rows {
		if row.Role != string(execution.ActivationCurrent) || !owned[row.QueryGroup] {
			continue
		}
		key := string(row.QueryGroup)
		g := byGroup[key]
		if g == nil {
			g = &observability.CostGroup{QueryGroupKey: key, SnapshotRevision: string(row.Publication.SnapshotRevision), QueryRevision: string(row.QueryRevision), ScheduleRevision: string(row.ScheduleRevision)}
			byGroup[key] = g
		}
		g.Members = append(g.Members, observability.CostPlanIdentity{TenantID: row.Identity.TenantID, BusinessID: row.Identity.BusinessID, StrategyID: row.Identity.StrategyID})
		g.TotalMembers++
	}
	keys := make([]string, 0, len(byGroup))
	for key := range byGroup {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	groups := make([]observability.CostGroup, 0, len(keys))
	for _, key := range keys {
		groups = append(groups, *byGroup[key])
	}
	return groups, snapshot.Complete && len(byGroup) == len(owned)
}
