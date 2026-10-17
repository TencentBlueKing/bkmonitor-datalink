// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License").

package elasticsearchstore

import (
	"context"
	"fmt"
	"net/http"
)

// RefreshActiveAlerts 只为新依赖索引的就绪回填建立搜索可见边界，不用于逐事件写入。
func (r *Repository) RefreshActiveAlerts(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	targets, err := r.router.ActiveAlertTargets(ctx)
	if err != nil {
		return err
	}
	targets, err = normalizeTargets(targets, r.config.MaxReadTargets)
	if err != nil {
		return err
	}
	var response struct {
		Shards *struct {
			Total      int `json:"total"`
			Successful int `json:"successful"`
			Failed     int `json:"failed"`
		} `json:"_shards"`
	}
	if err := r.performJSON(ctx, http.MethodPost, "/"+joinTargets(targets)+"/_refresh", nil, nil, &response); err != nil {
		return err
	}
	if response.Shards == nil || response.Shards.Total < 1 || response.Shards.Successful < 1 || response.Shards.Failed != 0 {
		return fmt.Errorf("refresh active alerts: incomplete shard result %+v", response.Shards)
	}
	return nil
}
