// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"log/slog"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	enrichruntime "linkd/internal/enrich/assembly"
	"linkd/internal/enrich/preview"
	"linkd/internal/eventsource"
	storeassembly "linkd/internal/store/assembly"
)

// newEnrichPreview 使用无 schema 初始化的事件读取与共享丰富运行时。
func newEnrichPreview(sources *eventsource.Service, storage config.StorageConfig, resources config.ResourcesConfig, loggers ...*slog.Logger) *preview.Service {
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	return preview.New(sources, func(ctx context.Context, tenant, id string) (domain.Event, error) {
		r, err := storeassembly.OpenReadOnly(ctx, storage, 4)
		if err != nil {
			return domain.Event{}, err
		}
		defer func() { _ = r.Close() }()
		a, err := r.Repository.GetEvent(ctx, tenant, id)
		return a.Event, err
	}, func(ctx context.Context, source config.EventSource) (preview.Enricher, func() error, error) {
		r, err := enrichruntime.Open(ctx, source, resources, 4, 5*time.Second, nil)
		if err != nil {
			return nil, nil, err
		}
		options := []enrichruntime.RouterOption{}
		if logger != nil {
			options = append(options, enrichruntime.WithLogger(logger))
		}
		router, err := r.Router(source, nil, options...)
		if err != nil {
			_ = r.Close()
			return nil, nil, err
		}
		return router, r.Close, nil
	})
}
