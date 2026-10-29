// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package suppressioncleanup

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"linkd/internal/policy"
)

// Document 是终态清理记录的持久快照；Version 仅用于该条记录的条件写入。
type Document struct {
	// ID 包含租户编码前缀，与单项 Journal 使用完全相同的持久键。
	ID string
	// Payload 只保存清理阶段和有界诊断，不保存告警正文。
	Payload json.RawMessage
	// Version 是后端条件写版本，创建时为空。
	Version string
	// Err 是此成员的持久化结果，不代表同批其他成员失败。
	Err error
}

// BatchDocuments 按显式成员块读取和写入清理记录，每项保留独立错误和版本。
type BatchDocuments interface {
	ReadCleanupBatch(context.Context, []string) []Document
	WriteCleanupBatch(context.Context, []Document) []Document
}

// RunBatch 按批保存 pending、执行清理、保存 completed，不为成员逐条读取或写入 Journal。
// 记录与 Redis 无跨系统事务，失败后仍以相同 Cause 重试，已完成项不再清理。
func (j *Journal) RunBatch(ctx context.Context, causes []Cause, run func(context.Context, []Cause) ([]Result, []error)) []error {
	results := make([]error, len(causes))
	docs, ok := j.docs.(BatchDocuments)
	if !ok {
		for i, c := range causes {
			results[i] = j.Run(ctx, c, func(call context.Context) (Result, error) { r, e := run(call, []Cause{c}); return r[0], e[0] })
		}
		return results
	}
	ids := make([]string, len(causes))
	for i, c := range causes {
		ids[i] = prefix(c.TenantID) + c.ID()
	}
	current := docs.ReadCleanupBatch(ctx, ids)
	records := make([]Record, len(causes))
	pending, positions := []Document{}, []int{}
	for i, c := range causes {
		row := current[i]
		var r Record
		switch {
		case errors.Is(row.Err, policy.ErrNotFound):
			r = Record{ID: c.ID(), Cause: c, State: "pending", StartedAt: j.now().Round(0).UTC()}
		case row.Err != nil:
			results[i] = row.Err
			continue
		default:
			if len(row.Payload) > MaxRecordBytes || json.Unmarshal(row.Payload, &r) != nil || r.Validate() != nil {
				results[i] = policy.ErrInvalid
				continue
			}
			if !reflect.DeepEqual(r.Cause, c) {
				results[i] = policy.ErrConflict
				continue
			}
			if r.State == "completed" {
				continue
			}
			r.PreviousUnconfirmed = true
		}
		if r.Validate() != nil {
			results[i] = policy.ErrInvalid
			continue
		}
		raw, err := json.Marshal(r)
		if err != nil {
			results[i] = err
			continue
		}
		records[i] = r
		positions = append(positions, i)
		pending = append(pending, Document{ID: ids[i], Payload: raw, Version: row.Version})
	}
	if len(pending) == 0 {
		return results
	}
	saved := docs.WriteCleanupBatch(ctx, pending)
	selected, owners := []Cause{}, []int{}
	for j, i := range positions {
		if saved[j].Err != nil {
			results[i] = saved[j].Err
		} else {
			selected = append(selected, causes[i])
			owners = append(owners, j)
		}
	}
	if len(selected) == 0 {
		return results
	}
	cleaned, errs := run(ctx, selected)
	completed, completedPositions := []Document{}, []int{}
	for k, position := range owners {
		i := positions[position]
		results[i] = errs[k]
		if results[i] == nil {
			results[i] = ctx.Err()
		}
		if results[i] != nil {
			continue
		}
		if cleaned[k].validate(causes[i]) != nil {
			results[i] = policy.ErrInvalid
			continue
		}
		r := records[i]
		r.Result = &cleaned[k]
		r.State = "completed"
		at := j.now().Round(0).UTC()
		if at.Before(r.StartedAt) {
			at = r.StartedAt
		}
		r.FinishedAt = &at
		raw, err := json.Marshal(r)
		if err != nil {
			results[i] = err
			continue
		}
		completed = append(completed, Document{ID: ids[i], Payload: raw, Version: saved[position].Version})
		completedPositions = append(completedPositions, i)
	}
	if len(completed) > 0 {
		written := docs.WriteCleanupBatch(ctx, completed)
		for k, i := range completedPositions {
			results[i] = written[k].Err
		}
	}
	return results
}
