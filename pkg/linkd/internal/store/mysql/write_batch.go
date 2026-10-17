// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package mysqlstore

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

type alertWrite struct {
	current store.StoredAlert
	next    domain.Alert
	payload string
	field   store.AlertChangeField
}

type writeCall struct {
	ctx   context.Context
	alert *alertWrite
	logs  []domain.AlertLog
	done  chan writeResult
	bytes int
}

type writeResult struct {
	alert store.StoredAlert
	logs  []store.AppendAlertLogItemResult
	err   error
}

// writeBatch 只合并已经准备好的仓储写入，不改变生命周期步骤与锁范围。
// 同一调用方等待自身提交结果；取消后的提交结果可能不确定，由原 CAS/日志身份恢复。
type writeBatch struct {
	ctx                context.Context
	cancel             context.CancelFunc
	queue              chan *writeCall
	done               chan struct{}
	repository         *Repository
	maxCalls, maxBytes int
	timeout            time.Duration
	workers            chan struct{}
	wg                 sync.WaitGroup
}

// EnableWriteBatch 返回独立的有界写入合批仓储。Close 必须在关闭数据库前调用。
// 单项走直接 UPDATE；多项只在短事务内批读版本并批量 UPDATE，冲突项独立返回。
func (r *Repository) EnableWriteBatch(concurrency, maxBytes int, timeout time.Duration) (*Repository, func(), error) {
	if concurrency < 1 || maxBytes < 1 || timeout <= 0 {
		return nil, nil, fmt.Errorf("invalid mysql batch resource limits")
	}
	ctx, cancel := context.WithCancel(context.Background())
	direct := *r
	direct.batch = nil
	b := &writeBatch{ctx: ctx, cancel: cancel, queue: make(chan *writeCall, concurrency), done: make(chan struct{}), repository: &direct, maxCalls: min(128, concurrency), maxBytes: maxBytes, timeout: timeout, workers: make(chan struct{}, concurrency)}
	copy := *r
	copy.batch = b
	go b.run()
	return &copy, func() { cancel(); <-b.done }, nil
}

func (b *writeBatch) submit(call *writeCall) writeResult {
	call.done = make(chan writeResult, 1)
	select {
	case b.queue <- call:
	case <-call.ctx.Done():
		return writeResult{err: call.ctx.Err()}
	case <-b.ctx.Done():
		return writeResult{err: b.ctx.Err()}
	}
	select {
	case result := <-call.done:
		return result
	case <-call.ctx.Done():
		return writeResult{err: call.ctx.Err()}
	case <-b.ctx.Done():
		return writeResult{err: b.ctx.Err()}
	}
}

func (b *writeBatch) run() {
	defer close(b.done)
	defer b.wg.Wait()
	var pending *writeCall
	for {
		first := pending
		pending = nil
		if first == nil {
			select {
			case first = <-b.queue:
			case <-b.ctx.Done():
				return
			}
		}
		calls, size := []*writeCall{first}, first.bytes
		timer := time.NewTimer(time.Millisecond)
	collect:
		for len(calls) < b.maxCalls && size < b.maxBytes {
			select {
			case call := <-b.queue:
				if size+call.bytes > b.maxBytes {
					pending = call
					break collect
				}
				calls = append(calls, call)
				size += call.bytes
			case <-timer.C:
				break collect
			case <-b.ctx.Done():
				timer.Stop()
				return
			}
		}
		timer.Stop()
		select {
		case b.workers <- struct{}{}:
		case <-b.ctx.Done():
			return
		}
		b.wg.Go(func() {
			defer func() { <-b.workers }()
			ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
			defer cancel()
			b.execute(ctx, calls)
		})
	}
}

func (b *writeBatch) execute(ctx context.Context, calls []*writeCall) {
	alerts, logCalls := []*writeCall{}, []*writeCall{}
	logs := []domain.AlertLog{}
	for _, call := range calls {
		if err := call.ctx.Err(); err != nil {
			call.done <- writeResult{err: err}
			continue
		}
		if call.alert != nil {
			alerts = append(alerts, call)
		} else {
			logCalls = append(logCalls, call)
			logs = append(logs, call.logs...)
		}
	}
	if len(alerts) > 0 {
		b.writeAlerts(ctx, alerts)
	}
	// 每个日志调用保留原顺序及结果；物理 INSERT 可以跨独立成员合并。
	if len(logs) > 0 {
		results := make([]store.AppendAlertLogItemResult, 0, len(logs))
		var err error
		for start := 0; start < len(logs); start += store.MaxBatchSize {
			var items []store.AppendAlertLogItemResult
			items, err = b.repository.AppendAlertLogs(ctx, logs[start:min(start+store.MaxBatchSize, len(logs))])
			if err != nil {
				break
			}
			results = append(results, items...)
		}
		offset := 0
		for _, call := range logCalls {
			result := writeResult{err: err}
			if err == nil {
				result.logs = results[offset : offset+len(call.logs)]
			}
			offset += len(call.logs)
			call.done <- result
		}
	}
}

func (b *writeBatch) writeAlerts(ctx context.Context, calls []*writeCall) {
	if len(calls) == 1 {
		stored, err := b.repository.writeAlert(ctx, *calls[0].alert)
		calls[0].done <- writeResult{alert: stored, err: err}
		return
	}
	// 1. 一次锁定批内实际行并读取轻量版本，避免每条 BEGIN/SELECT/COMMIT。
	// 这里只锁短 SQL 提交阶段，Hook/Redis/日志不进入事务。
	phaseAt := time.Now()
	phase := func(name string) {
		now := time.Now()
		store.ObserveBatchPhase(ctx, "sql."+name, now.Sub(phaseAt))
		phaseAt = now
	}
	tx, err := b.repository.db.BeginTx(ctx, nil)
	phase("begin")
	if err != nil {
		failWriteCalls(calls, err)
		return
	}
	defer rollback(tx)
	where, args := make([]string, 0, len(calls)), make([]any, 0, len(calls)*2)
	for _, call := range calls {
		a := call.alert.current.Alert
		where = append(where, "(bk_tenant_id=? AND alert_id=?)")
		args = append(args, a.BKTenantID, a.AlertID)
	}
	//nolint:gosec // G202: where 只含固定条件占位符，租户和 ID 都是绑定参数。
	rows, err := tx.QueryContext(ctx, "SELECT bk_tenant_id,alert_id,version FROM linkd_alerts WHERE "+strings.Join(where, " OR ")+" ORDER BY bk_tenant_id,alert_id FOR UPDATE", args...)
	if err != nil {
		failWriteCalls(calls, err)
		return
	}
	type key struct{ tenant, id string }
	versions := map[key]uint64{}
	for rows.Next() {
		var k key
		var v uint64
		if err = rows.Scan(&k.tenant, &k.id, &v); err != nil {
			break
		}
		versions[k] = v
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr := rows.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		failWriteCalls(calls, err)
		return
	}
	phase("select_lock")
	// 2. 冲突成员不参加更新；同一行最多接受一次，保留每项独立的 CAS 结果。
	accepted := make([]*writeCall, 0, len(calls))
	for _, call := range calls {
		a := call.alert.current.Alert
		k := key{a.BKTenantID, a.AlertID}
		v, _ := parseVersion(call.alert.current.Version)
		if versions[k] != v {
			call.done <- writeResult{err: store.ErrVersionConflict}
			continue
		}
		if err := call.ctx.Err(); err != nil {
			call.done <- writeResult{err: err}
			continue
		}
		delete(versions, k)
		accepted = append(accepted, call)
	}
	if len(accepted) == 0 {
		return
	}
	selects, args := make([]string, 0, len(accepted)), []any{}
	for _, call := range accepted {
		w := call.alert
		v, _ := parseVersion(w.current.Version)
		var endType, endAt, active any
		if w.next.EndAt != nil {
			endType, endAt = w.next.EndType, w.next.EndAt.UnixNano()
		}
		if !w.next.Status.Terminal() {
			active = 1
		}
		selects = append(selects, "SELECT ? AS tenant,? AS id,? AS expected,? AS body,? AS field,? AS status,? AS latest,? AS end_type,? AS end_at,? AS active,? AS policy_work,? AS merge_work")
		args = append(args, w.next.BKTenantID, w.next.AlertID, v, w.payload, string(w.field), w.next.Status, w.next.LatestEventID, endType, endAt, active, store.HasShieldWork(w.next), store.HasMergeWork(w.next))
	}
	//nolint:gosec // G202: selects 只含固定 SELECT 占位符，不拼接业务值。
	query := `UPDATE linkd_alerts AS a JOIN (` + strings.Join(selects, " UNION ALL ") + `) AS b ON a.bk_tenant_id=b.tenant AND a.alert_id=b.id AND a.version=b.expected SET a.payload=CASE WHEN b.field='' THEN CAST(b.body AS JSON) ELSE JSON_REMOVE(a.payload,CONCAT('$.',b.field)) END,a.version=a.version+1,a.status=b.status,a.latest_event_id=b.latest,a.end_type=b.end_type,a.end_at_ns=b.end_at,a.active_marker=b.active,a.policy_work=b.policy_work,a.merge_work=b.merge_work`
	phase("encode")
	_, err = tx.ExecContext(ctx, query, args...)
	phase("update")
	// 3. 只有提交确认后才返回成功；提交响应丢失时原流程重读/重试。
	if err == nil {
		err = tx.Commit()
		phase("commit")
	}
	if err != nil {
		if isDuplicateKey(err) {
			err = fmt.Errorf("%w: active fingerprint", store.ErrIdentityConflict)
		}
		failWriteCalls(accepted, err)
		return
	}
	for _, call := range accepted {
		v, _ := parseVersion(call.alert.current.Version)
		call.done <- writeResult{alert: store.StoredAlert{Alert: call.alert.next, Version: versionToken(v + 1)}}
	}
}

func failWriteCalls(calls []*writeCall, err error) {
	for _, call := range calls {
		call.done <- writeResult{err: err}
	}
}
