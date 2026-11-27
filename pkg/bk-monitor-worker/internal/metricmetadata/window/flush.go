// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package window

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func (p *Processor) flushWithRouteRefresh(ctx context.Context, refreshed bool) error {
	flushStarted := time.Now()
	start := p.now()
	if p.pending.Len() > 0 {
		defer func() {
			metrics.MetricMetadataBatchDuration.WithLabelValues(p.dataID).Observe(p.now().Sub(start).Seconds())
		}()
		filterStarted := time.Now()
		records, cachedDuplicates := p.filterCachedDuplicates(p.pending.Records(), start)
		filterDuration := time.Since(filterStarted)
		aggregatedRecords := len(records)
		metrics.MetricMetadataBatchRecords.WithLabelValues(p.dataID).Observe(float64(aggregatedRecords))
		groupingStarted := time.Now()
		byGroup := make(map[int]map[string][]core.Record)
		for _, r := range records {
			if byGroup[r.GroupID] == nil {
				byGroup[r.GroupID] = make(map[string][]core.Record)
			}
			byGroup[r.GroupID][r.ScopeHash] = append(byGroup[r.GroupID][r.ScopeHash], r)
		}
		groups := make([]int, 0, len(byGroup))
		for id := range byGroup {
			groups = append(groups, id)
		}
		sort.Ints(groups)
		groupingDuration := time.Since(groupingStarted)
		applyDuration := time.Duration(0)
		createdValues, changedScopes, countChanges, droppedRecords := 0, 0, 0, 0
		for _, id := range groups {
			hashes := make([]string, 0, len(byGroup[id]))
			for h := range byGroup[id] {
				hashes = append(hashes, h)
			}
			sort.Strings(hashes)
			var batch []core.Record
			for _, h := range hashes {
				batch = append(batch, byGroup[id][h]...)
			}
			applyStarted := time.Now()
			result, err := p.store.Apply(ctx, id, batch, start)
			groupApplyDuration := time.Since(applyStarted)
			applyDuration += groupApplyDuration
			if err != nil {
				logger.Debugf("metric metadata store apply finished dataid=%s group_id=%d scopes=%d records=%d duration=%s outcome=error err=%v", p.dataID, id, len(byGroup[id]), len(batch), groupApplyDuration, err)
				p.clearCache("write_failure")
				if errors.Is(err, storage.ErrGroupUnavailable) && !refreshed {
					if refreshErr := p.routes.Refresh(ctx, p.store); refreshErr != nil {
						metrics.MetricMetadataStorageErrors.WithLabelValues(p.dataID, "routes").Inc()
						return fmt.Errorf("refresh metric routes after group change: %w", refreshErr)
					}
					routed, routeErr := p.routeRecords(ctx, p.pending.Ordered())
					if routeErr != nil {
						return routeErr
					}
					p.pending.Replace(routed)
					return p.flushWithRouteRefresh(ctx, true)
				}
				metrics.MetricMetadataStorageErrors.WithLabelValues(p.dataID, "apply").Inc()
				return err
			}
			for reason, n := range result.Dropped {
				metrics.MetricMetadataRecords.WithLabelValues(p.dataID, reason).Add(float64(n))
			}
			if len(result.Dropped) > 0 {
				logger.Warnf("metric metadata records dropped during persistence dataid=%s group_id=%d input_records=%d dropped=%v", p.dataID, id, len(batch), result.Dropped)
			}
			metrics.MetricMetadataRecords.WithLabelValues(p.dataID, "duplicate").Add(float64(result.DuplicateRecords))
			createdValues += result.CreatedValues
			changedScopes += result.ChangedScopes
			countChanges += result.CountChanges
			droppedRecords += sum(result.Dropped)
			metrics.MetricMetadataRecords.WithLabelValues(p.dataID, "persisted").Add(float64(len(batch) - sum(result.Dropped)))
			metrics.MetricMetadataScopes.WithLabelValues(p.dataID, "members").Add(float64(result.ChangedScopes))
			metrics.MetricMetadataScopes.WithLabelValues(p.dataID, "count").Add(float64(result.CountChanges))
			for _, st := range result.Scopes {
				p.remember(st)
			}
		}
		// Clear only after all group transactions succeed. On failure the full
		// batch remains available for replay, including already committed chunks.
		p.pending.Reset()
		if cachedDuplicates > 0 {
			count := float64(cachedDuplicates)
			metrics.MetricMetadataRecords.WithLabelValues(p.dataID, "duplicate").Add(count)
			metrics.MetricMetadataRecords.WithLabelValues(p.dataID, "persisted").Add(count)
		}
		scopeCount := 0
		for _, scopes := range byGroup {
			scopeCount += len(scopes)
		}
		logger.Debugf("metric metadata batch persisted dataid=%s aggregated_records=%d cached_duplicates=%d routed_scopes=%d groups=%d created_values=%d changed_scopes=%d count_changes=%d dropped_records=%d filter_duration=%s grouping_duration=%s store_apply_duration=%s duration=%s wall_duration=%s", p.dataID, aggregatedRecords, cachedDuplicates, scopeCount, len(groups), createdValues, changedScopes, countChanges, droppedRecords, filterDuration, groupingDuration, applyDuration, p.now().Sub(start), time.Since(flushStarted))
	}
	p.updateCacheMetrics()
	return nil
}

// filterCachedDuplicates skips records already present in the committed scope
// snapshot. Cache misses and expired touch intervals still go through MySQL.
func (p *Processor) filterCachedDuplicates(records []core.Record, now time.Time) ([]core.Record, int) {
	var filtered []core.Record
	duplicates := 0
	for i, record := range records {
		state, ok := p.stateWindow.Lookup(record.Key().ScopeKey)
		if !ok || now.Sub(state.LastSeen) >= p.opts.TouchInterval {
			if filtered != nil {
				filtered = append(filtered, record)
			}
			continue
		}
		metric, ok := state.Metrics[record.FieldName]
		if !ok || metric.Fingerprint != record.Fingerprint {
			if filtered != nil {
				filtered = append(filtered, record)
			}
			continue
		}
		if filtered == nil {
			filtered = make([]core.Record, 0, len(records)-1)
			filtered = append(filtered, records[:i]...)
		}
		duplicates++
	}
	if filtered == nil {
		return records, 0
	}
	return filtered, duplicates
}
