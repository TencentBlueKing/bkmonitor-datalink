// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	lifecycleprocess "linkd/internal/lifecycle/process"
	"linkd/internal/store"
)

const closeReadBatchSize = 1000

type closeAlertsInput struct {
	closeAlertInput
	Filter json.RawMessage `json:"filter"`
	DryRun bool            `json:"dry_run,omitempty"`
}

type closeAlertsItem struct {
	AlertID       string `json:"alert_id"`
	Status        int    `json:"status"`
	AlreadyClosed bool   `json:"already_closed,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
	Message       string `json:"message,omitempty"`
}

type closeAlertsResponse struct {
	Items     []closeAlertsItem `json:"items"`
	HasMore   bool              `json:"has_more"`
	DryRun    bool              `json:"dry_run"`
	Closed    int               `json:"closed"`
	Failed    int               `json:"failed"`
	ErrorCode string            `json:"error_code,omitempty"`
	Message   string            `json:"message,omitempty"`
}

type closeBatchCompletion struct {
	start    int
	outcomes []lifecycle.CloseAlertOutcome
	duration time.Duration
}

func (a *API) closeAlertsByFilter(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	service := a.AlertSearch
	if service == nil {
		http.Error(w, "alert search unavailable", http.StatusServiceUnavailable)
		return
	}
	if service.reader == nil {
		http.Error(w, "filtered close requires Elasticsearch storage", http.StatusNotImplemented)
		return
	}
	var input closeAlertsInput
	if err := decodeAlertQuery(w, r, service.config.MaxRequestBytes, &input); err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "invalid filtered close request", status)
		return
	}
	q := store.AlertSearchRequest{TenantID: input.TenantID, Query: input.Filter, Limit: closeReadBatchSize, IDsOnly: true}
	if len(input.Filter) == 0 || !validAlertQuery(q) || (!input.DryRun && !input.validOperation()) {
		http.Error(w, "invalid filtered close request", http.StatusBadRequest)
		return
	}
	closer, ok := a.AlertCloser.(interface {
		CloseAlerts(context.Context, []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome
	})
	if !input.DryRun && !ok {
		http.Error(w, "batch close is not configured", http.StatusServiceUnavailable)
		return
	}
	query, err := json.Marshal(map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]any{"status": domain.AlertStatusActive}}, input.Filter}}})
	if err != nil {
		http.Error(w, "invalid close filter", http.StatusBadRequest)
		return
	}
	q.Query = query
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	concurrency := a.CloseConcurrency
	if concurrency == 0 {
		concurrency = (config.CloseControlConfig{}).WithDefaults().Concurrency
	}
	done := make(chan closeBatchCompletion, concurrency)
	result := closeAlertsResponse{Items: []closeAlertsItem{}, DryRun: input.DryRun}
	running, completed := 0, 0
	estimate, spent := 8*time.Second, time.Duration(0)
	exhausted, stopped := false, false
	finish := func(batch closeBatchCompletion) {
		running--
		completed++
		spent += batch.duration
		estimate = spent / time.Duration(completed)
		for j, outcome := range batch.outcomes {
			item := &result.Items[batch.start+j]
			item.AlreadyClosed = outcome.AlreadyClosed
			if outcome.Err == nil {
				item.Status = 200
				result.Closed++
				continue
			}
			item.Status, item.ErrorCode, item.Message = 502, "result_uncertain", "close result uncertain; retry the same operation"
			var classified *lifecycleprocess.CloseError
			if errors.As(outcome.Err, &classified) {
				item.Status, item.ErrorCode, item.Message = classified.Status, classified.Code, classified.Message
			}
			result.Failed++
		}
	}
	for !exhausted || running > 0 {
		// 先吸收已完成块的耗时，再判断能否启动新块；不会因尚未读取完成通知而使用旧估计。
	drain:
		for running > 0 {
			select {
			case batch := <-done:
				finish(batch)
			default:
				break drain
			}
		}
		fits := closeBatchFitsBudget(ctx, estimate)
		if input.DryRun {
			fits = ctx.Err() == nil
		}
		if !exhausted && !stopped && running < concurrency && fits {
			at := time.Now()
			queryCtx, queryCancel := context.WithTimeout(ctx, time.Duration(service.config.TimeoutSeconds)*time.Second)
			page, queryErr := service.reader.SearchAlerts(queryCtx, q)
			queryCancel()
			if queryErr != nil {
				if len(result.Items) == 0 {
					service.failure(w, queryCtx, input.TenantID, queryErr)
					return
				}
				service.logger.WarnContext(ctx, "close candidate search failed", "module", "controlplane", "operation", "close_filter", "error", queryErr, "bk_tenant_id", input.TenantID)
				result.ErrorCode, result.Message = "query_failed", "candidate search failed; continue with the same operation"
				stopped = true
				result.HasMore = true
			} else {
				start := len(result.Items)
				for _, alert := range page.Items {
					result.Items = append(result.Items, closeAlertsItem{AlertID: alert.AlertID, Status: 200})
				}
				exhausted = page.Next == "" || len(page.Items) == 0
				q.After = page.Next
				if input.DryRun {
					continue
				}
				if !closeBatchFitsBudget(ctx, estimate) {
					for i := start; i < len(result.Items); i++ {
						result.Items[i].Status, result.Items[i].ErrorCode, result.Items[i].Message = 504, "not_attempted", "remaining request budget is below estimated batch duration; continue with the same operation"
						result.Failed++
					}
					stopped = true
					result.HasMore = true
				} else if len(page.Items) > 0 {
					commands := make([]lifecycle.CloseAlertCommand, len(page.Items))
					for i, alert := range page.Items {
						commands[i] = input.command(alert.AlertID)
					}
					running++
					// 每个请求拥有自己的批次池；块内不再叠加成员并发，也不占用查询或全局关闭名额。
					go func() {
						out := closer.CloseAlerts(ctx, commands)
						done <- closeBatchCompletion{start: start, outcomes: out, duration: time.Since(at)}
					}()
					continue
				}
			}
		} else if !exhausted && !fits {
			stopped = true
			result.HasMore = true
		}
		if running > 0 {
			finish(<-done)
		} else {
			break
		}
		if stopped && running == 0 {
			break
		}
	}
	// 顶层 200 表示返回实际处理结果。未扫描的告警由下次同条件调用继续；失败项保留 Alert ID 与原操作重试语义。
	output(w, result)
}

func closeBatchFitsBudget(ctx context.Context, estimate time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	deadline, ok := ctx.Deadline()
	return !ok || time.Until(deadline) > estimate
}

func (input closeAlertsInput) validOperation() bool {
	command := input.command("")
	source := command.OperationSource
	if source == "" {
		source = "manual"
		if command.OperatorKind == domain.OperatorKindSystem {
			source = "system"
		}
	}
	operation := domain.AlertEndOperation{ID: command.OperationID, OperatorKind: command.OperatorKind, OperatorID: command.OperatorID, Source: source}
	return operation.Validate() == nil && input.Reason != "" && len(input.Reason) <= 256 && !input.EffectiveAt.IsZero() && !input.EffectiveAt.After(time.Now().Add(5*time.Minute))
}
