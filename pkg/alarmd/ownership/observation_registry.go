// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package ownership

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
)

// ObservationRegistryLimits bounds one diagnostic read, independently of the
// control-plane reader. Commands counts submitted commands, so the diagnostic
// client must disable automatic retries and bound its transport timeouts.
type ObservationRegistryLimits struct {
	Bytes    int64
	Commands int
	Rows     int
	Timeout  time.Duration
}

// ObservationRegistrySnapshot is a read window over existing registrations,
// not an atomic membership snapshot. Total counts registry candidates whose
// scores are after At; it is NOT a ready-worker denominator. Total is -1 if
// ZCOUNT was unavailable. A ready denominator is known only when Complete is
// true; otherwise callers must report UNKNOWN even if ReadyIDs is nonempty.
type ObservationRegistrySnapshot struct {
	At           time.Time `json:"at"`
	ReadyIDs     []string  `json:"ready_ids"`
	Total        int64     `json:"total"`
	Offset       int64     `json:"offset"`
	NextOffset   int64     `json:"next_offset"`
	ScannedRows  int       `json:"scanned_rows"`
	ReadBytes    int64     `json:"read_bytes"`
	ReadCommands int       `json:"read_commands"`
	Complete     bool      `json:"complete"`
	Reason       string    `json:"reason,omitempty"`
}

// ReadObservationRegistry uses a caller-owned diagnostic client and only the
// existing registry/worker keys. It never cleans expired or missing members.
// ReadBytes counts returned member strings and registration bytes, excluding
// RESP framing and the ZCOUNT integer. GETRANGE stays within the remaining
// budget; a response filling that budget is conservatively incomplete.
//
// ZRANGEBYSCORE is bounded by Rows and Commands. Redis cannot byte-limit each
// member in that command, and registration currently has no worker-ID length
// limit. An oversized member reply can therefore exceed Bytes; it is reported
// in ReadBytes and stops all further reads. This is a row bound on discovery,
// not a hard wire-byte bound for arbitrary registry members.
func (store *RedisStore) ReadObservationRegistry(
	ctx context.Context, reader redis.Cmdable, at time.Time, offset int64, limits ObservationRegistryLimits,
) ObservationRegistrySnapshot {
	result := ObservationRegistrySnapshot{At: at, ReadyIDs: []string{}, Total: -1, Offset: offset, NextOffset: offset}
	if store == nil || store.prefix == "" || reader == nil || at.IsZero() || offset < 0 {
		result.Reason = "invalid_request"
		return result
	}
	if limits.Bytes <= 0 || limits.Commands <= 0 || limits.Rows <= 0 || limits.Timeout <= 0 {
		result.Reason = "disabled"
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()
	if ctx.Err() != nil {
		result.Reason = "timeout"
		return result
	}
	minimum := "(" + strconv.FormatInt(at.UnixMilli(), 10)
	result.ReadCommands++
	total, err := reader.ZCount(ctx, store.workerRegistryKey(), minimum, "+inf").Result()
	if err != nil || ctx.Err() != nil {
		result.Reason = observationRegistryReadReason(ctx)
		return result
	}
	if total < 0 {
		result.Reason = "registry_changed"
		return result
	}
	result.Total = total
	if total == 0 {
		result.Offset, result.NextOffset, result.Complete = 0, 0, true
		return result
	}
	if offset >= total {
		offset = 0
		result.Offset, result.NextOffset = 0, 0
	}
	// Reserve one command for the member page, then one per worker document.
	count := min(int64(limits.Rows), total-offset, int64(limits.Commands-result.ReadCommands-1))
	if count <= 0 {
		result.Reason = "commands_budget"
		return result
	}
	if ctx.Err() != nil {
		result.Reason = "timeout"
		return result
	}
	result.ReadCommands++
	ids, err := reader.ZRangeByScore(ctx, store.workerRegistryKey(), &redis.ZRangeBy{
		Min: minimum, Max: "+inf", Offset: offset, Count: count,
	}).Result()
	for _, id := range ids {
		result.ReadBytes += int64(len(id))
	}
	if err != nil || ctx.Err() != nil {
		result.Reason = observationRegistryReadReason(ctx)
		return result
	}
	if int64(len(ids)) != count {
		result.Reason = "registry_changed"
		if int64(len(ids)) > count {
			return result
		}
	}
	if result.ReadBytes >= limits.Bytes {
		result.Reason = "bytes_budget"
		// Skip this discovery page on the next rotation, including an oversized
		// ID which cannot be fetched within this read's byte budget.
		result.NextOffset = offset + int64(len(ids))
		if result.NextOffset >= total {
			result.NextOffset = 0
		}
		return result
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			result.Reason = "timeout"
			return result
		}
		remaining := limits.Bytes - result.ReadBytes
		if remaining <= 0 {
			result.Reason = "bytes_budget"
			return result
		}
		result.ReadCommands++
		result.ScannedRows++
		result.NextOffset = offset + int64(result.ScannedRows)
		if result.NextOffset >= total {
			result.NextOffset = 0
		}
		payload, readErr := reader.GetRange(ctx, store.workerKey(id), 0, remaining-1).Result()
		result.ReadBytes += int64(len(payload))
		if readErr != nil || ctx.Err() != nil {
			result.Reason = observationRegistryReadReason(ctx)
			return result
		}
		if int64(len(payload)) >= remaining {
			result.Reason = "bytes_budget"
			return result
		}
		if payload == "" {
			result.Reason = "missing_worker"
			continue
		}
		var worker WorkerRegistration
		if json.Unmarshal([]byte(payload), &worker) != nil || worker.Validate() != nil || worker.WorkerID != id {
			result.Reason = "invalid_worker"
			continue
		}
		if worker.AssignmentReadiness == WorkerReady && worker.ExpiresAt.After(at) {
			result.ReadyIDs = append(result.ReadyIDs, id)
		}
	}
	if ctx.Err() != nil {
		result.Reason = "timeout"
	} else if result.Reason == "" {
		switch {
		case offset != 0:
			result.Reason = "offset_window"
		case count < total && count == int64(limits.Rows):
			result.Reason = "rows_budget"
		case count < total:
			result.Reason = "commands_budget"
		default:
			result.Complete = true
		}
	}
	return result
}

func observationRegistryReadReason(ctx context.Context) string {
	if ctx.Err() != nil {
		return "timeout"
	}
	return "read_error"
}
