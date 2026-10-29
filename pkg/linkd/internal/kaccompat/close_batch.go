// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package kaccompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

type closeStateWriter interface {
	WriteCloseBatch(context.Context, []domain.Alert) []WriteResult
}

// ProjectBatchNow 在涉及的通道内一次批读最新事实并 Bulk refresh，逐项返回写入结果。
// 按通道固定顺序加锁，避免两个关闭批次交叉等待；普通批次随后重新读取，不能写回旧状态。
func (r *Runtime) ProjectBatchNow(ctx context.Context, tenant string, ids []string) []error {
	results := make([]error, len(ids))
	fail := func(err error) []error {
		for i := range results {
			results[i] = err
		}
		return results
	}
	lanes := map[*projectionLane]bool{}
	for _, id := range ids {
		lanes[r.lane(tenant, id)] = true
	}
	for lane := range lanes {
		lane.urgent.Add(1)
		defer lane.urgent.Add(-1)
	}
	started := time.Now()
	for i := range r.lanes {
		lane := &r.lanes[i]
		if !lanes[lane] {
			continue
		}
		select {
		case lane.gate <- struct{}{}:
			defer func() { <-lane.gate }()
		case <-ctx.Done():
			return fail(ctx.Err())
		}
	}
	store.ObserveBatchPhase(ctx, "projection.lane_wait", time.Since(started))
	if len(ids) == 0 {
		return results
	}
	started = time.Now()
	read := r.alerts.GetAlertsCurrent
	compactWriter, compact := r.writer.(closeStateWriter)
	if states, ok := r.alerts.(store.AlertCloseStateBatchStore); compact && ok {
		read = states.GetAlertCloseStates
	} else {
		compact = false
	}
	current, err := read(ctx, tenant, ids)
	store.ObserveBatchPhase(ctx, "projection.read", time.Since(started))
	if err != nil {
		return fail(err)
	}
	byID := map[string]domain.Alert{}
	for _, a := range current.Alerts {
		byID[a.Alert.AlertID] = a.Alert
	}
	alerts, positions := []domain.Alert{}, []int{}
	for i, id := range ids {
		if a, ok := byID[id]; ok {
			alerts, positions = append(alerts, a), append(positions, i)
		} else {
			results[i] = store.ErrNotFound
		}
	}
	if len(alerts) > 0 {
		started = time.Now()
		var written []WriteResult
		if compact {
			written = compactWriter.WriteCloseBatch(ctx, alerts)
		} else {
			written = r.writer.WriteBatch(ctx, alerts, true)
		}
		// 只有 KAC 文档尚不存在时才需要完整快照创建；正常关闭不回读 Enrich 或正文。
		missing, missingPositions := []string{}, []int{}
		for j, result := range written {
			if compact && errors.Is(result.Err, store.ErrNotFound) {
				missing, missingPositions = append(missing, alerts[j].AlertID), append(missingPositions, j)
			}
		}
		if len(missing) > 0 {
			full, err := r.alerts.GetAlertsCurrent(ctx, tenant, missing)
			byID := map[string]domain.Alert{}
			for _, a := range full.Alerts {
				byID[a.Alert.AlertID] = a.Alert
			}
			creates, createPositions := []domain.Alert{}, []int{}
			for j, id := range missing {
				if err != nil {
					written[missingPositions[j]].Err = err
				} else if a, found := byID[id]; found {
					creates, createPositions = append(creates, a), append(createPositions, missingPositions[j])
				}
			}
			if len(creates) > 0 {
				created := r.writer.WriteBatch(ctx, creates, true)
				for j, position := range createPositions {
					written[position] = created[j]
				}
			}
		}
		store.ObserveBatchPhase(ctx, "projection.write", time.Since(started))
		for j, i := range positions {
			results[i] = written[j].Err
		}
	}
	return results
}

// WriteCloseBatch 仅更新现存 KAC 文档的终态字段；不存在项交给调用方按需补读后创建。
func (c *Client) WriteCloseBatch(ctx context.Context, alerts []domain.Alert) []WriteResult {
	results := make([]WriteResult, len(alerts))
	keys := make([]alarmKey, len(alerts))
	for i, a := range alerts {
		keys[i] = alarmKey{tenant: a.BKTenantID, alert: a.AlertID}
	}
	locations, err := c.locate(ctx, keys)
	if err != nil {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	var raw bytes.Buffer
	positions := []int{}
	flush := func() {
		if len(positions) == 0 {
			return
		}
		var response struct {
			Items []map[string]struct {
				Status int    `json:"status"`
				Index  string `json:"_index"`
			} `json:"items"`
		}
		err := c.requestRaw(ctx, http.MethodPost, "/_bulk", url.Values{"refresh": {"true"}}, raw.Bytes(), "application/x-ndjson", &response)
		for j, i := range positions {
			if err != nil {
				results[i].Err = err
				continue
			}
			if j >= len(response.Items) {
				results[i].Err = fmt.Errorf("KAC close bulk response missing item")
				continue
			}
			item := response.Items[j]["update"]
			if item.Status == http.StatusNotFound {
				results[i].Err = store.ErrNotFound
				c.mu.Lock()
				delete(c.locations, keys[i])
				c.mu.Unlock()
			} else if item.Status >= 300 {
				results[i].Err = httpFailure{item.Status}
			} else {
				results[i].Index = item.Index
			}
		}
		raw.Reset()
		positions = positions[:0]
	}
	for i, a := range alerts {
		index, found := locations[keys[i]]
		if !found {
			results[i].Err = store.ErrNotFound
			continue
		}
		head, _ := json.Marshal(map[string]any{"update": map[string]any{"_index": index, "_id": a.AlertID}})
		closeTime := any(nil)
		if a.EndAt != nil {
			closeTime = a.EndAt.In(kacZone).Format(kacTimeLayout)
		}
		body, err := json.Marshal(map[string]any{"doc": map[string]any{"status": closedStatus(a), "action": "close", "source_alarm_status": "close", "close_reason": a.EndReason, "close_time": closeTime}})
		if err != nil {
			results[i].Err = err
			continue
		}
		if raw.Len() > 0 && raw.Len()+len(head)+len(body)+2 > c.config.Delivery.MaxBatchBytes {
			flush()
		}
		raw.Write(head)
		raw.WriteByte('\n')
		raw.Write(body)
		raw.WriteByte('\n')
		positions = append(positions, i)
	}
	flush()
	return results
}

func closedStatus(a domain.Alert) string {
	status := "system_closed"
	switch a.EndType {
	case domain.AlertEndTypeUser:
		status = "closed"
	case domain.AlertEndTypeSource, domain.AlertEndTypeSeverityUpgrade:
		status = "source_closed"
	}
	if a.EndOperation != nil {
		switch a.EndOperation.Source {
		case "auto_policy":
			status = "auto_closed"
		case "self_heal":
			status = "autoexecute_closed"
		case "work_order":
			status = "autoorder_closed"
		}
	}
	return status
}
