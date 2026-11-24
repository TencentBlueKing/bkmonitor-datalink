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
	"errors"
	"fmt"
	"log/slog"

	"linkd/internal/config"
	"linkd/internal/dynamicconfig"
	snapshotstore "linkd/internal/dynamicconfig/storage"
	"linkd/internal/runtimeconfig"
)

// openDynamicConfig 的禁用分支必须先于仓储和来源构造，保证默认测试不增加 I/O。
func openDynamicConfig(ctx context.Context, cfg config.Config, state *runtimeconfig.Severity, logger *slog.Logger) (*dynamicconfig.Manager, func() error, error) {
	closeNone := func() error { return nil }
	if cfg.ControlPlane == nil || cfg.ControlPlane.DynamicConfig == nil || !cfg.ControlPlane.DynamicConfig.Enabled {
		return nil, closeNone, nil
	}
	c := *cfg.ControlPlane.DynamicConfig
	if err := c.Validate(); err != nil {
		return nil, closeNone, err
	}
	if cfg.Storage == nil {
		return nil, closeNone, fmt.Errorf("dynamic config requires Linkd snapshot storage")
	}
	store, err := snapshotstore.New(*cfg.Storage)
	if err != nil {
		return nil, closeNone, err
	}
	b := *c.Bindings.Severity
	source, err := dynamicconfig.OpenSource(c.Sources[b.Source], b)
	if err != nil {
		return nil, closeNone, errors.Join(err, store.Close())
	}
	closeAll := func() error { return errors.Join(source.Close(), store.Close()) }
	manager, err := dynamicconfig.New(c, cfg.Dispatch.WithDefaults().Deployment, cfg.Severity, state, source, store, logger)
	if err != nil {
		return nil, closeNone, errors.Join(err, closeAll())
	}
	manager.Bootstrap(ctx)
	return manager, closeAll, nil
}

func prepareDynamicConfigSnapshots(ctx context.Context, cfg config.Config) error {
	if cfg.ControlPlane == nil || cfg.ControlPlane.DynamicConfig == nil || !cfg.ControlPlane.DynamicConfig.Enabled {
		return nil
	}
	if cfg.Storage == nil {
		return fmt.Errorf("dynamic config requires Linkd snapshot storage")
	}
	store, err := snapshotstore.New(*cfg.Storage)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	return store.EnsureSchema(ctx)
}
