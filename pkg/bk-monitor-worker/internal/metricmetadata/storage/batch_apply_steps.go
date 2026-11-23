// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// applyBatch 仅在单个 group 事务内存活，承载各写入阶段共享的状态。
type applyBatch struct {
	store   *MySQLStore
	ctx     context.Context
	tx      *sql.Tx
	groupID int
	now     time.Time
	records []core.Record
	result  Result

	reportScopeTotal int
	reportValueTotal int
	dbScopeTotal     int
	touchedScopes    int
	referencedIDs    int
	insertedValues   int
	deltaValues      int

	reportScopes        map[string]reportScopeRow
	reportValueIDsCache map[reportValueKey]uint32
	reportValueByID     map[uint32]reportValue
	reportValueByKey    map[reportValueKey]reportValue
	inputKeys           []reportValueKey
	accepted            []core.Record
	missing             map[reportValueKey]core.Record
	initialScopeCounts  map[reportValueKey]int
	timing              applyTiming
}

type applyTiming struct {
	beginTx, groupLock, scopeCount, loadScopes                 time.Duration
	cacheLockWait, cacheLockHold, cacheLookup, loadScopeValues time.Duration
	lookupValues, prepareRecords, valueCount, insertValues     time.Duration
	loadInsertedValues, buildPlans, writeScopes, referenceLoad time.Duration
	referenceUpdate, commit, cacheUpdate                       time.Duration
}

func (b *applyBatch) loadScopes() error {
	if b.store.maxReportScopes > 0 {
		started := time.Now()
		if err := b.tx.QueryRowContext(b.ctx, `SELECT COUNT(*) FROM metadata_timeseriesreportscope WHERE group_id=?`, b.groupID).Scan(&b.reportScopeTotal); err != nil {
			b.timing.scopeCount = time.Since(started)
			return err
		}
		b.timing.scopeCount = time.Since(started)
	}
	b.dbScopeTotal = b.reportScopeTotal

	hashSet := make(map[string]bool)
	var hashes []string
	for _, record := range b.records {
		if !hashSet[record.ScopeHash] {
			hashSet[record.ScopeHash] = true
			hashes = append(hashes, record.ScopeHash)
		}
	}
	sort.Strings(hashes)
	args := []any{b.groupID}
	for _, hash := range hashes {
		args = append(args, hash)
	}
	b.reportScopes = make(map[string]reportScopeRow)
	started := time.Now()
	rows, err := b.tx.QueryContext(b.ctx, `SELECT id,scope_hash,report_value_ids,value_count,last_seen,version
	FROM metadata_timeseriesreportscope WHERE group_id=? AND scope_hash IN (`+strings.TrimSuffix(strings.Repeat("?,", len(hashes)), ",")+`) ORDER BY id`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var state core.ScopeState
		var hash string
		var blob []byte
		var valueCount int
		if err = rows.Scan(&state.ID, &hash, &blob, &valueCount, &state.LastSeen, &state.Version); err != nil {
			break
		}
		reportValueIDs, decodeErr := core.DecodeIDs(blob, valueCount)
		if decodeErr != nil {
			err = decodeErr
			break
		}
		state.Key = core.ScopeKey{GroupID: b.groupID, Hash: hash}
		state.Metrics = make(map[string]core.MetricState, valueCount)
		b.reportScopes[hash] = reportScopeRow{state: state, reportValueIDs: reportValueIDs}
	}
	if err == nil {
		err = rows.Err()
	}
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	b.timing.loadScopes = time.Since(started)
	return err
}

func (b *applyBatch) loadScopeValues() error {
	started := time.Now()
	reportValueIDs := make(map[uint32]struct{})
	for _, row := range b.reportScopes {
		for _, id := range row.reportValueIDs {
			reportValueIDs[id] = struct{}{}
		}
	}
	b.referencedIDs = len(reportValueIDs)
	b.reportValueIDsCache = make(map[reportValueKey]uint32, len(reportValueIDs))
	b.reportValueByID = make(map[uint32]reportValue, len(reportValueIDs))
	b.reportValueByKey = make(map[reportValueKey]reportValue, len(reportValueIDs))
	if len(reportValueIDs) > 0 {
		members, err := loadReportValuesByIDs(b.ctx, b.tx, reportValueIDs)
		if err != nil {
			return err
		}
		for id := range reportValueIDs {
			member, ok := members[id]
			if !ok {
				return fmt.Errorf("missing value %d referenced by a persisted scope", id)
			}
			if member.group != b.groupID {
				return fmt.Errorf("value %d belongs to group %d, expected %d", id, member.group, b.groupID)
			}
			value := reportValue{id: int64(id), field: member.field, fp: member.fp, reportScopeCount: member.reportScopeCount}
			key := reportValueKey{value.field, value.fp}
			b.reportValueByID[id] = value
			b.reportValueByKey[key] = value
			b.reportValueIDsCache[key] = id
		}
		if len(members) != len(reportValueIDs) {
			return fmt.Errorf("persisted scope references a missing value")
		}
	}
	for hash, row := range b.reportScopes {
		row.state.Metrics = make(map[string]core.MetricState, len(row.reportValueIDs))
		for _, id := range row.reportValueIDs {
			value, ok := b.reportValueByID[id]
			if !ok {
				return fmt.Errorf("missing value %d referenced by a persisted scope", id)
			}
			if _, exists := row.state.Metrics[value.field]; exists {
				return fmt.Errorf("duplicate metric in persisted scope")
			}
			row.state.Metrics[value.field] = core.MetricState{ValueID: id, Fingerprint: value.fp}
		}
		b.reportScopes[hash] = row
	}
	b.timing.loadScopeValues = time.Since(started)
	return nil
}

func (b *applyBatch) loadInputValues() error {
	started := time.Now()
	keySet := make(map[reportValueKey]struct{}, len(b.records))
	keys := make([]reportValueKey, 0, len(b.records))
	for _, record := range b.records {
		if record.GroupID != b.groupID {
			return fmt.Errorf("record group does not match transaction")
		}
		key := reportValueKey{record.FieldName, record.Fingerprint}
		if _, ok := b.reportValueByKey[key]; ok {
			continue
		}
		if _, seen := keySet[key]; !seen {
			keySet[key] = struct{}{}
			keys = append(keys, key)
		}
	}

	lockStarted := time.Now()
	b.store.cacheMu.Lock()
	b.timing.cacheLockWait = time.Since(lockStarted)
	lockStarted = time.Now()
	uncached := make([]reportValueKey, 0, len(keys))
	invalidCachedID := false
	for _, key := range keys {
		if id, ok := b.store.reportValueIDCache[b.groupID][key]; ok {
			if id == 0 {
				invalidCachedID = true
				break
			}
			value := reportValue{id: int64(id), field: key.field, fp: key.fp}
			b.reportValueByKey[key] = value
			b.reportValueByID[id] = value
		} else {
			uncached = append(uncached, key)
		}
	}
	b.timing.cacheLockHold += time.Since(lockStarted)
	b.store.cacheMu.Unlock()
	b.timing.cacheLookup = time.Since(started)
	if invalidCachedID {
		return fmt.Errorf("cached report value ID is invalid")
	}

	b.inputKeys = uncached
	sort.Slice(b.inputKeys, func(i, j int) bool {
		if b.inputKeys[i].field == b.inputKeys[j].field {
			return b.inputKeys[i].fp < b.inputKeys[j].fp
		}
		return b.inputKeys[i].field < b.inputKeys[j].field
	})
	started = time.Now()
	values, err := readReportValuesByKeys(b.ctx, b.tx, b.groupID, b.inputKeys)
	b.timing.lookupValues = time.Since(started)
	if err != nil {
		return err
	}
	for _, value := range values {
		if value.id <= 0 || value.id > math.MaxUint32 {
			return fmt.Errorf("persisted value ID outside uint32 range")
		}
		key := reportValueKey{value.field, value.fp}
		b.reportValueByKey[key] = value
		b.reportValueByID[uint32(value.id)] = value
		b.reportValueIDsCache[key] = uint32(value.id)
	}
	return nil
}

func (b *applyBatch) admitRecords() ([]reportValueKey, error) {
	if b.store.maxReportValues > 0 {
		started := time.Now()
		if err := b.tx.QueryRowContext(b.ctx, `SELECT COUNT(*) FROM metadata_timeseriesreportvalue WHERE group_id=?`, b.groupID).Scan(&b.reportValueTotal); err != nil {
			b.timing.valueCount = time.Since(started)
			return nil, err
		}
		b.timing.valueCount = time.Since(started)
	}

	started := time.Now()
	newScopes := make(map[string]struct{})
	reservedValues := make(map[reportValueKey]struct{})
	b.missing = make(map[reportValueKey]core.Record)
	b.accepted = make([]core.Record, 0, len(b.records))
	for _, record := range b.records {
		key := reportValueKey{record.FieldName, record.Fingerprint}
		_, scopeExists := b.reportScopes[record.ScopeHash]
		if !scopeExists {
			if _, reserved := newScopes[record.ScopeHash]; !reserved && b.store.maxReportScopes > 0 && b.reportScopeTotal+len(newScopes) >= b.store.maxReportScopes {
				b.result.Dropped["scope_limit"]++
				continue
			}
		}
		_, valueExists := b.reportValueByKey[key]
		_, valueReserved := reservedValues[key]
		if !valueExists && !valueReserved && b.store.maxReportValues > 0 && b.reportValueTotal+len(reservedValues) >= b.store.maxReportValues {
			b.result.Dropped["value_limit"]++
			continue
		}
		b.accepted = append(b.accepted, record)
		if !scopeExists {
			newScopes[record.ScopeHash] = struct{}{}
		}
		if !valueExists && !valueReserved {
			reservedValues[key] = struct{}{}
			b.missing[key] = record
		}
		if old, exists := b.reportScopes[record.ScopeHash].state.Metrics[record.FieldName]; exists && old.Fingerprint == record.Fingerprint {
			b.result.DuplicateRecords++
		}
	}

	keys := make([]reportValueKey, 0, len(b.missing))
	for key := range b.missing {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].field == keys[j].field {
			return keys[i].fp < keys[j].fp
		}
		return keys[i].field < keys[j].field
	})
	b.initialScopeCounts = make(map[reportValueKey]int, len(keys))
	insertedKeys := make(map[reportValueKey]struct{}, len(keys))
	for _, key := range keys {
		insertedKeys[key] = struct{}{}
	}
	for _, record := range b.accepted {
		key := reportValueKey{record.FieldName, record.Fingerprint}
		if _, ok := insertedKeys[key]; ok {
			b.initialScopeCounts[key]++
		}
	}
	b.insertedValues = len(keys)
	b.timing.prepareRecords = time.Since(started)
	return keys, nil
}

func (b *applyBatch) insertValues(keys []reportValueKey) error {
	started := time.Now()
	for start := 0; start < len(keys); start += sqlBatchSize {
		end := min(start+sqlBatchSize, len(keys))
		var query strings.Builder
		args := make([]any, 0, (end-start)*8)
		query.WriteString(`INSERT INTO metadata_timeseriesreportvalue (group_id,field_name,fingerprint,original_name,original_type,metric_type,meta,scope_count) VALUES `)
		for i, key := range keys[start:end] {
			if i > 0 {
				query.WriteByte(',')
			}
			query.WriteString("(?,?,?,?,?,?,?,?)")
			record := b.missing[key]
			args = append(args, b.groupID, record.FieldName, record.Fingerprint, record.OriginalName, record.OriginalType, record.MetricType, record.Meta, b.initialScopeCounts[key])
		}
		if _, err := b.tx.ExecContext(b.ctx, query.String(), args...); err != nil {
			b.timing.insertValues = time.Since(started)
			return err
		}
	}
	b.timing.insertValues = time.Since(started)

	if len(keys) == 0 {
		return nil
	}
	started = time.Now()
	values, err := readReportValuesByKeys(b.ctx, b.tx, b.groupID, keys)
	b.timing.loadInsertedValues = time.Since(started)
	if err != nil {
		return err
	}
	for _, value := range values {
		if value.id <= 0 || value.id > math.MaxUint32 {
			return fmt.Errorf("new value ID exceeds uint32 encoding range")
		}
		key := reportValueKey{value.field, value.fp}
		if _, ok := b.missing[key]; ok {
			b.reportValueByKey[key] = value
			b.reportValueByID[uint32(value.id)] = value
			b.reportValueIDsCache[key] = uint32(value.id)
		}
	}
	b.result.CreatedValues = len(keys)
	return nil
}

func (b *applyBatch) writeScopes() (map[uint32]int, error) {
	started := time.Now()
	plans := make(map[string]core.ScopeState)
	originals := make(map[string]core.ScopeState)
	info := make(map[string]core.Record)
	for _, record := range b.accepted {
		value, ok := b.reportValueByKey[reportValueKey{record.FieldName, record.Fingerprint}]
		if !ok {
			b.result.Dropped["value_limit"]++
			continue
		}
		state, ok := plans[record.ScopeHash]
		if !ok {
			state = b.reportScopes[record.ScopeHash].state.Clone()
			if state.Metrics == nil {
				state.Metrics = make(map[string]core.MetricState)
			}
			state.Key = core.ScopeKey{GroupID: b.groupID, Hash: record.ScopeHash}
			originals[record.ScopeHash] = b.reportScopes[record.ScopeHash].state
		}
		state.Metrics[record.FieldName] = core.MetricState{ValueID: uint32(value.id), Fingerprint: value.fp}
		plans[record.ScopeHash] = state
		info[record.ScopeHash] = record
	}
	b.timing.buildPlans = time.Since(started)

	started = time.Now()
	delta := make(map[uint32]int)
	hashes := make([]string, 0, len(plans))
	for hash := range plans {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	for _, hash := range hashes {
		state := plans[hash]
		old := originals[hash]
		valueDelta := core.ReferenceDelta(old, state)
		changed := state.ID == 0 || len(valueDelta) > 0
		if changed || b.now.Sub(state.LastSeen) >= b.store.touchInterval {
			blob, err := core.EncodeIDs(state.IDs())
			if err != nil {
				b.timing.writeScopes = time.Since(started)
				return nil, err
			}
			if state.ID == 0 {
				record := info[hash]
				res, err := b.tx.ExecContext(b.ctx, `INSERT INTO metadata_timeseriesreportscope
				(group_id,scope_hash,scope_type,labels,raw_scope,report_value_ids,value_count,last_seen,version) VALUES (?,?,?,?,?,?,?,?,0)`,
					b.groupID, hash, record.ScopeType, record.Labels, record.RawScope, blob, len(state.Metrics), b.now)
				if err != nil {
					b.timing.writeScopes = time.Since(started)
					return nil, err
				}
				state.ID, err = res.LastInsertId()
				if err != nil {
					b.timing.writeScopes = time.Since(started)
					return nil, err
				}
			} else {
				res, err := b.tx.ExecContext(b.ctx, `UPDATE metadata_timeseriesreportscope SET report_value_ids=?,value_count=?,last_seen=?,version=version+1 WHERE group_id=? AND id=? AND version=?`, blob, len(state.Metrics), b.now, b.groupID, state.ID, state.Version)
				if err != nil {
					b.timing.writeScopes = time.Since(started)
					return nil, err
				}
				n, err := res.RowsAffected()
				if err != nil {
					b.timing.writeScopes = time.Since(started)
					return nil, err
				}
				if n != 1 {
					b.timing.writeScopes = time.Since(started)
					return nil, errConflict
				}
				state.Version++
			}
			state.LastSeen = b.now
			if changed {
				b.result.ChangedScopes++
			}
			if len(old.Metrics) != len(state.Metrics) {
				b.result.CountChanges++
			}
		}
		state.LoadedAt = b.now
		b.result.Scopes = append(b.result.Scopes, state)
		for id, change := range valueDelta {
			delta[id] += change
		}
	}
	b.deltaValues = len(delta)
	for _, key := range b.initialScopeCountsKeys() {
		value := b.reportValueByKey[key]
		id := uint32(value.id)
		if delta[id] != b.initialScopeCounts[key] {
			b.timing.writeScopes = time.Since(started)
			return nil, fmt.Errorf("new value %d reference count mismatch: inserted=%d delta=%d", id, b.initialScopeCounts[key], delta[id])
		}
		delete(delta, id)
	}
	b.touchedScopes = len(plans)
	b.timing.writeScopes = time.Since(started)
	return delta, nil
}

func (b *applyBatch) initialScopeCountsKeys() []reportValueKey {
	keys := make([]reportValueKey, 0, len(b.initialScopeCounts))
	for key := range b.initialScopeCounts {
		keys = append(keys, key)
	}
	return keys
}

func (b *applyBatch) updateReferences(delta map[uint32]int) error {
	started := time.Now()
	values, err := loadReportReferenceValues(b.ctx, b.tx, b.groupID, delta, b.reportValueByID)
	b.timing.referenceLoad = time.Since(started)
	if err != nil {
		return err
	}
	started = time.Now()
	err = bumpReferences(b.ctx, b.tx, b.groupID, delta, values)
	b.timing.referenceUpdate = time.Since(started)
	return err
}

func (b *applyBatch) publishCache() {
	started := time.Now()
	b.store.cacheMu.Lock()
	lockStarted := time.Now()
	b.timing.cacheLockWait += time.Since(started)
	cached := b.store.reportValueIDCache[b.groupID]
	if cached == nil {
		cached = make(map[reportValueKey]uint32, len(b.reportValueIDsCache))
	}
	for key, id := range b.reportValueIDsCache {
		cached[key] = id
	}
	b.store.reportValueIDCache[b.groupID] = cached
	b.timing.cacheLockHold += time.Since(lockStarted)
	b.store.cacheMu.Unlock()
	b.timing.cacheUpdate = time.Since(started)
}

func (b *applyBatch) logTiming(started time.Time, outcome string) {
	t := b.timing
	logger.Debugf("metric metadata MySQL apply stage timing group_id=%d records=%d duration=%s outcome=%s begin_tx=%s lock_group=%s count_scopes=%s load_scopes=%s cache_lock_wait=%s cache_lock_hold=%s cache_lookup=%s load_scope_values=%s lookup_input_values=%s prepare_records=%s count_values=%s insert_values=%s load_inserted_values=%s build_plans=%s write_scopes=%s load_reference_values=%s update_references=%s commit=%s cache_update=%s db_scope_total=%d touched_scopes=%d scope_value_ids=%d inserted_values=%d delta_values=%d",
		b.groupID, len(b.records), time.Since(started), outcome, t.beginTx, t.groupLock, t.scopeCount, t.loadScopes,
		t.cacheLockWait, t.cacheLockHold, t.cacheLookup, t.loadScopeValues, t.lookupValues, t.prepareRecords, t.valueCount,
		t.insertValues, t.loadInsertedValues, t.buildPlans, t.writeScopes, t.referenceLoad,
		t.referenceUpdate, t.commit, t.cacheUpdate, b.dbScopeTotal, b.touchedScopes, b.referencedIDs, b.insertedValues, b.deltaValues)
}
