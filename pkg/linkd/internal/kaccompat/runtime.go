// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/processingerror"
	"linkd/internal/store"
)

// AlertReader 批量读取实时告警；普通 ES 搜索不能替代刚保存后的实时读取。
type AlertReader interface {
	GetAlertsCurrent(context.Context, string, []string) (store.AlertBatch, error)
}

// Writer 只负责一次批量写入与动作批次刷新。
type Writer interface {
	WriteBatch(context.Context, []domain.Alert, bool) []WriteResult
	Refresh(context.Context, []string) error
}

// Observer 只使用阶段和结果标签，业务身份记录在日志中。
type Observer interface {
	KACResult(context.Context, string, error, time.Duration, int)
	KACQueue(context.Context, int64, time.Duration)
}

type queuedChange struct {
	id     string
	change domain.AlertChange
}

type actionJob struct {
	request ActionRequest
	index   string
	entry   queuedChange
}

type projectionLane struct {
	items  chan queuedChange
	gate   chan struct{}
	urgent atomic.Int64
}

// Runtime 是控制面的常驻队列消费者。停止信号只停止接收和启动新任务，
// 已开始的 I/O 使用独立超时上下文，等待完成后才退出；未开始消息保留在 Stream 的 PEL 中。
type Runtime struct {
	cfg       config.KACDeliveryConfig
	redis     redis.UniversalClient
	stream    string
	alerts    AlertReader
	writer    Writer
	sender    ActionSender
	observer  Observer
	logger    *slog.Logger
	lanes     []projectionLane
	actions   chan actionJob
	ready     chan actionJob
	completed chan string
}

// NewRuntime 不启动 goroutine，所有运行资源由 Run 和调用方管理。
func NewRuntime(cfg config.KACDeliveryConfig, client redis.UniversalClient, stream string, alerts AlertReader, writer Writer, sender ActionSender, observer Observer, logger *slog.Logger) *Runtime {
	cfg = cfg.WithDefaults()
	r := &Runtime{cfg: cfg, redis: client, stream: stream, alerts: alerts, writer: writer, sender: sender, observer: observer, logger: logger, lanes: make([]projectionLane, cfg.ProjectionConcurrency), actions: make(chan actionJob, cfg.ActionBuffer), ready: make(chan actionJob, cfg.ActionConcurrency), completed: make(chan string, cfg.BatchSize*cfg.ProjectionConcurrency*2)}
	for i := range r.lanes {
		r.lanes[i].items = make(chan queuedChange, cfg.BatchSize*2)
		r.lanes[i].gate = make(chan struct{}, 1)
	}
	return r
}

func (r *Runtime) lane(tenant, alert string) *projectionLane {
	h := fnv.New64a()
	_, _ = h.Write([]byte(tenant + "\x00" + alert))
	return &r.lanes[h.Sum64()%uint64(len(r.lanes))]
}

// ProjectNow 优先取得所属通道，读取最新事实并同步刷新；等待中的普通批次随后重新读取最新告警。
func (r *Runtime) ProjectNow(ctx context.Context, tenant, alert string) error {
	lane := r.lane(tenant, alert)
	lane.urgent.Add(1)
	defer lane.urgent.Add(-1)
	select {
	case lane.gate <- struct{}{}:
		defer func() { <-lane.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	batch, err := r.alerts.GetAlertsCurrent(ctx, tenant, []string{alert})
	if err != nil {
		return err
	}
	if len(batch.Alerts) == 0 {
		return store.ErrNotFound
	}
	result := r.writer.WriteBatch(ctx, []domain.Alert{batch.Alerts[0].Alert}, true)
	return result[0].Err
}

// Run 复用同一 Consumer 身份恢复上次未完成消息，不维护第二份任务状态。
func (r *Runtime) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for {
		err := r.redis.XGroupCreateMkStream(ctx, r.stream, "kac", "0").Err()
		if err == nil || strings.Contains(err.Error(), "BUSYGROUP") {
			break
		}
		if ctx.Err() != nil {
			return nil
		}
		r.report(ctx, "read", err, domain.AlertChange{}, 0, 1)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
	var workers sync.WaitGroup
	for i := range r.lanes {
		lane := &r.lanes[i]
		workers.Go(func() { r.runLane(ctx, lane) })
	}
	workers.Go(func() { r.runRefresh(ctx) })
	for range r.cfg.ActionConcurrency {
		workers.Go(func() { r.runAction(ctx) })
	}
	workers.Go(func() { r.observeQueue(ctx) })
	settled := make(chan struct{})
	go func() { r.runSettlements(); close(settled) }()
	defer func() { cancel(); workers.Wait(); close(r.completed); <-settled }()
	cursor := "0"
	for ctx.Err() == nil {
		streams, readErr := r.redis.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "kac", Consumer: "control-plane", Streams: []string{r.stream, cursor}, Count: int64(r.cfg.BatchSize), Block: 100 * time.Millisecond}).Result()
		if errors.Is(readErr, redis.Nil) {
			if cursor != ">" {
				cursor = ">"
			}
			continue
		}
		if readErr != nil {
			if ctx.Err() != nil {
				break
			}
			r.report(ctx, "read", readErr, domain.AlertChange{}, 0, 1)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
			continue
		}
		count := 0
		for _, stream := range streams {
			for _, entry := range stream.Messages {
				count++
				if cursor != ">" {
					cursor = entry.ID
				}
				var change domain.AlertChange
				raw, _ := entry.Values["body"].(string)
				if err := json.Unmarshal([]byte(raw), &change); err != nil {
					r.report(ctx, "decode", err, change, 0, 1)
					r.ack(entry.ID)
					continue
				}
				select {
				case r.lane(change.BKTenantID, change.AlertID).items <- queuedChange{entry.ID, change}:
				case <-ctx.Done():
					return nil
				}
			}
		}
		if count == 0 {
			cursor = ">"
		}
	}
	return nil
}

func (r *Runtime) runLane(stop context.Context, lane *projectionLane) {
	for {
		if stop.Err() != nil {
			return
		}
		var first queuedChange
		select {
		case <-stop.Done():
			return
		case first = <-lane.items:
		}
		entries := []queuedChange{first}
		timer := time.NewTimer(time.Duration(r.cfg.BatchWaitMilliseconds) * time.Millisecond)
	collect:
		for len(entries) < r.cfg.BatchSize {
			select {
			case <-stop.Done():
				timer.Stop()
				return
			case item := <-lane.items:
				entries = append(entries, item)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		for lane.urgent.Load() > 0 {
			select {
			case <-stop.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
		select {
		case lane.gate <- struct{}{}:
		case <-stop.Done():
			return
		}
		if stop.Err() != nil {
			<-lane.gate
			return
		}
		call, cancel := context.WithTimeout(context.WithoutCancel(stop), 10*time.Second)
		r.process(call, entries)
		cancel()
		<-lane.gate
	}
}

func (r *Runtime) process(ctx context.Context, entries []queuedChange) {
	started := time.Now()
	groups := map[string][]string{}
	seen := map[string]bool{}
	for _, entry := range entries {
		change := entry.change
		key := change.BKTenantID + "\x00" + change.AlertID
		if !seen[key] {
			groups[change.BKTenantID] = append(groups[change.BKTenantID], change.AlertID)
			seen[key] = true
		}
	}
	alerts := []domain.Alert{}
	outcomes := map[string]WriteResult{}
	for tenant, ids := range groups {
		batch, err := processingerror.Guard(func() (store.AlertBatch, error) { return r.alerts.GetAlertsCurrent(ctx, tenant, ids) })
		if err != nil {
			for _, id := range ids {
				outcomes[tenant+"\x00"+id] = WriteResult{Err: err}
			}
			continue
		}
		for _, row := range batch.Alerts {
			alerts = append(alerts, row.Alert)
		}
		for _, id := range batch.NotFound {
			outcomes[tenant+"\x00"+id] = WriteResult{Err: store.ErrNotFound}
		}
	}
	if len(alerts) > 0 {
		results, err := processingerror.Guard(func() ([]WriteResult, error) { return r.writer.WriteBatch(ctx, alerts, false), nil })
		for i, alert := range alerts {
			if err != nil {
				outcomes[alert.BKTenantID+"\x00"+alert.AlertID] = WriteResult{Err: err}
			} else {
				outcomes[alert.BKTenantID+"\x00"+alert.AlertID] = results[i]
			}
		}
	}
	for _, entry := range entries {
		change := entry.change
		result, ok := outcomes[change.BKTenantID+"\x00"+change.AlertID]
		if !ok {
			result.Err = store.ErrNotFound
		}
		r.report(ctx, "projection", result.Err, change, time.Since(started), 1)
		if result.Err != nil || change.Action == nil {
			r.ack(entry.id)
			continue
		}
		job := actionJob{request: ActionRequest{SchemaVersion: "linkd.kac-action.v3", TenantID: change.BKTenantID, AlertID: change.AlertID, AlertAction: *change.Action}, index: result.Index, entry: entry}
		select {
		case r.actions <- job:
		default:
			r.report(ctx, "action_buffer", fmt.Errorf("KAC action buffer full"), change, 0, 1)
			r.ack(entry.id)
		}
	}
}

func (r *Runtime) runRefresh(stop context.Context) {
	for {
		if stop.Err() != nil {
			return
		}
		var first actionJob
		select {
		case <-stop.Done():
			return
		case first = <-r.actions:
		}
		jobs := []actionJob{first}
		timer := time.NewTimer(time.Duration(r.cfg.BatchWaitMilliseconds) * time.Millisecond)
	collect:
		for len(jobs) < r.cfg.BatchSize {
			select {
			case <-stop.Done():
				timer.Stop()
				return
			case item := <-r.actions:
				jobs = append(jobs, item)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		if stop.Err() != nil {
			return
		}
		indices := []string{}
		for _, job := range jobs {
			if !slices.Contains(indices, job.index) {
				indices = append(indices, job.index)
			}
		}
		call, cancel := context.WithTimeout(context.WithoutCancel(stop), 10*time.Second)
		_, err := processingerror.Guard(func() (struct{}, error) { return struct{}{}, r.writer.Refresh(call, indices) })
		cancel()
		for _, job := range jobs {
			if err != nil {
				r.report(context.WithoutCancel(stop), "refresh", err, job.entry.change, 0, 1)
				r.ack(job.entry.id)
				continue
			}
			select {
			case <-stop.Done():
				return
			case r.ready <- job:
			}
		}
	}
}

func (r *Runtime) runAction(stop context.Context) {
	for {
		if stop.Err() != nil {
			return
		}
		var job actionJob
		select {
		case <-stop.Done():
			return
		case job = <-r.ready:
		}
		if stop.Err() != nil {
			return
		}
		started := time.Now()
		call, cancel := context.WithTimeout(context.WithoutCancel(stop), 10*time.Second)
		_, err := processingerror.Guard(func() (struct{}, error) { return struct{}{}, r.sender.Send(call, job.request) })
		r.report(call, "action", err, job.entry.change, time.Since(started), 1)
		cancel()
		r.ack(job.entry.id)
	}
}

func (r *Runtime) ack(id string) { r.completed <- id }

// 消费确认按批次提交；只接收已经执行完成的消息。关闭时排空完成列表，
// 未开始执行的通知没有进入该列表，仍在 PEL 中等待下次启动。
func (r *Runtime) runSettlements() {
	for first := range r.completed {
		ids := []string{first}
		timer := time.NewTimer(time.Duration(r.cfg.BatchWaitMilliseconds) * time.Millisecond)
		closed := false
	collect:
		for len(ids) < r.cfg.BatchSize {
			select {
			case id, ok := <-r.completed:
				if !ok {
					closed = true
					break collect
				}
				ids = append(ids, id)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		call, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := r.redis.Pipelined(call, func(pipe redis.Pipeliner) error {
			pipe.XAck(call, r.stream, "kac", ids...)
			pipe.XDel(call, r.stream, ids...)
			return nil
		})
		if err != nil {
			r.report(call, "settle", err, domain.AlertChange{}, 0, len(ids))
		}
		cancel()
		if closed {
			return
		}
	}
}

func (r *Runtime) report(ctx context.Context, stage string, err error, change domain.AlertChange, duration time.Duration, count int) {
	if r.observer != nil {
		r.observer.KACResult(ctx, stage, err, duration, count)
	}
	if err != nil && r.logger != nil {
		actionID := ""
		if change.Action != nil {
			actionID = change.Action.ID
		}
		r.logger.WarnContext(ctx, "KAC output failed", "module", "kac", "operation", stage, "action_id", actionID, "stage", stage, "bk_tenant_id", change.BKTenantID, "alert_id", change.AlertID, "error", err.Error())
	}
}

func (r *Runtime) observeQueue(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			length, err := r.redis.XLen(ctx, r.stream).Result()
			if err != nil {
				continue
			}
			age := time.Duration(0)
			if rows, err := r.redis.XRangeN(ctx, r.stream, "-", "+", 1).Result(); err == nil && len(rows) > 0 {
				ms, _ := strconv.ParseInt(strings.SplitN(rows[0].ID, "-", 2)[0], 10, 64)
				age = time.Since(time.UnixMilli(ms))
			}
			if r.observer != nil {
				r.observer.KACQueue(ctx, length, age)
			}
		}
	}
}
