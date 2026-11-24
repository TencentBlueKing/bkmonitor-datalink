// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store"
)

// AlertSearcher 只读取原生告警，接收已校验的租户作用域、DSL 对象和分页参数。
type AlertSearcher interface {
	SearchAlerts(context.Context, store.AlertSearchRequest) (store.AlertSearchPage, error)
}

// AlertSearch 为管理检索提供独立并发槽，不与 Lifecycle 工作队列共享预算。
type AlertSearch struct {
	reader AlertSearcher
	config config.AlertSearchConfig
	logger *slog.Logger
	slots  chan struct{}
}

// NewAlertSearch 注入只读端口、已校验的部署预算和进程日志；连接仍由装配方释放。
// reader 为 nil 表示当前存储后端不支持 Elasticsearch Query DSL。
func NewAlertSearch(reader AlertSearcher, cfg config.AlertSearchConfig, logger *slog.Logger) *AlertSearch {
	cfg = cfg.WithDefaults()
	return &AlertSearch{reader: reader, config: cfg, logger: logger, slots: make(chan struct{}, cfg.MaxConcurrent)}
}

func (a *API) searchAlerts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	service := a.AlertSearch
	if service == nil {
		http.Error(w, "alert search unavailable", http.StatusServiceUnavailable)
		return
	}
	if service.reader == nil {
		http.Error(w, "alert search requires Elasticsearch storage", http.StatusNotImplemented)
		return
	}
	q := store.AlertSearchRequest{Limit: store.DefaultPageSize}
	if err := decodeAlertQuery(w, r, service.config.MaxRequestBytes, &q); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "alert search request too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "invalid alert search request", http.StatusBadRequest)
		}
		return
	}
	if !validAlertQuery(q) {
		http.Error(w, "invalid alert search request", http.StatusBadRequest)
		return
	}
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		http.Error(w, "alert search busy", http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(service.config.TimeoutSeconds)*time.Second)
	defer cancel()
	page, err := service.reader.SearchAlerts(ctx, q)
	if err != nil {
		service.failure(w, ctx, q.TenantID, err)
		return
	}
	payload, err := json.Marshal(page)
	if err == nil && len(payload) > service.config.MaxResponseBytes {
		err = fmt.Errorf("alert search page exceeds %d response bytes", service.config.MaxResponseBytes)
	}
	if err != nil {
		service.failure(w, ctx, q.TenantID, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}

func decodeAlertQuery(w http.ResponseWriter, r *http.Request, maximum int64, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maximum)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("trailing JSON")
	}
	return err
}

func validAlertQuery(q store.AlertSearchRequest) bool {
	query := bytes.TrimSpace(q.Query)
	limit := store.MaxPageSize
	if q.IDsOnly {
		limit = store.MaxCloseBatchSize
	}
	return domain.ValidateIdentityPart("bk_tenant_id", q.TenantID, 64) == nil && len(q.After) <= domain.EntityIDMaxBytes && q.Limit >= 1 && q.Limit <= limit && (len(query) == 0 || query[0] == '{')
}

func (s *AlertSearch) failure(w http.ResponseWriter, ctx context.Context, tenant string, err error) {
	status, message := http.StatusBadGateway, "alert search failed"
	switch {
	case errors.Is(err, store.ErrInvalidArgument):
		status, message = http.StatusBadRequest, "invalid alert query DSL"
	case errors.Is(err, context.DeadlineExceeded):
		status, message = http.StatusGatewayTimeout, "alert search timed out"
	}
	if !errors.Is(err, context.Canceled) {
		s.logger.WarnContext(ctx, "alert search failed", "module", "controlplane", "operation", "alert_search", "bk_tenant_id", tenant, "error", err)
	}
	http.Error(w, message, status)
}
