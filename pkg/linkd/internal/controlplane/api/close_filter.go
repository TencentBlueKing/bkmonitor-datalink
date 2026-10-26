// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"linkd/internal/domain"
	lifecycleprocess "linkd/internal/lifecycle/process"
	"linkd/internal/store"
)

type closeAlertsInput struct {
	closeAlertInput
	Filter json.RawMessage `json:"filter"`
	After  string          `json:"after,omitempty"`
	Limit  int             `json:"limit,omitempty"`
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
	Items  []closeAlertsItem `json:"items"`
	Next   string            `json:"next"`
	DryRun bool              `json:"dry_run"`
	Closed int               `json:"closed"`
	Failed int               `json:"failed"`
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
	input := closeAlertsInput{Limit: store.DefaultPageSize}
	if err := decodeAlertQuery(w, r, service.config.MaxRequestBytes, &input); err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "invalid filtered close request", status)
		return
	}
	q := store.AlertSearchRequest{TenantID: input.TenantID, Query: input.Filter, After: input.After, Limit: input.Limit}
	if len(input.Filter) == 0 || !validAlertQuery(q) || (!input.DryRun && !input.validOperation()) {
		http.Error(w, "invalid filtered close request", http.StatusBadRequest)
		return
	}
	if !input.DryRun && a.AlertCloser == nil {
		http.Error(w, "close is not configured", http.StatusServiceUnavailable)
		return
	}
	// 查询和关闭共用一个有界请求槽，逐条调用现有关闭器，避免每批额外放大并发。
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		http.Error(w, "alert search busy", http.StatusTooManyRequests)
		return
	}
	// 只选择当前可搜索的 active 候选；Filter 原样嵌套，不覆盖其 bool 语义。
	query, err := json.Marshal(map[string]any{"bool": map[string]any{"filter": []any{
		map[string]any{"term": map[string]any{"status": domain.AlertStatusActive}}, input.Filter,
	}}})
	if err != nil {
		http.Error(w, "invalid close filter", http.StatusBadRequest)
		return
	}
	q.Query = query
	queryCtx, cancel := context.WithTimeout(r.Context(), time.Duration(service.config.TimeoutSeconds)*time.Second)
	page, err := service.reader.SearchAlerts(queryCtx, q)
	cancel()
	if err != nil {
		service.failure(w, queryCtx, input.TenantID, err)
		return
	}
	result := closeAlertsResponse{Items: make([]closeAlertsItem, 0, len(page.Items)), Next: page.Next, DryRun: input.DryRun}
	for _, alert := range page.Items {
		item := closeAlertsItem{AlertID: alert.AlertID, Status: http.StatusOK}
		if input.DryRun {
			result.Items = append(result.Items, item)
			continue
		}
		if r.Context().Err() != nil {
			item.Status, item.ErrorCode, item.Message = http.StatusGatewayTimeout, "not_attempted", "request ended before this alert was attempted; retry the same operation"
		} else {
			// 过滤只用于选择候选，真正关闭仍由原流程实时读取并执行租约、CAS、Hook 与投影。
			// 同一批操作在各 Alert 上复用 operation_id；现有日志身份本来就包含 AlertID。
			closed, err := a.AlertCloser.CloseAlert(r.Context(), input.command(alert.AlertID))
			if err == nil {
				item.AlreadyClosed = closed.AlreadyClosed
				result.Closed++
			} else {
				item.Status, item.ErrorCode, item.Message = http.StatusBadGateway, "result_uncertain", "close result uncertain; retry the same operation"
				var classified *lifecycleprocess.CloseError
				if errors.As(err, &classified) {
					item.Status, item.ErrorCode, item.Message = classified.Status, classified.Code, classified.Message
				}
			}
		}
		if item.Status != http.StatusOK {
			result.Failed++
		}
		result.Items = append(result.Items, item)
	}
	// HTTP 200 只表示本批已枚举并返回逐项结果；失败和未执行项须用原命令按 AlertID 重试。
	// 不在调用中重新搜索已关闭目标，否则 active 过滤会遗漏 CAS 后尚待补齐输出的告警。
	output(w, result)
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
