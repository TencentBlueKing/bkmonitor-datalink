// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
)

// The dynamic group store a deployment runs serves a snapshot up to 10
// minutes old: the host index's own staleness bound, which decision-017
// section 3.1 gives the group store on purpose, with the host index's
// one-minute cadence beside it.
// The resolver's boundary is read against a store built with these numbers
// in cmdbcache; this is what says the deployed store is built with them.
func TestTheDeployedGroupStoreUsesTheTenMinuteStalenessBound(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	cfg := config.Default()
	prefix := "test_prefix:"
	cfg.PlatformCache.DynamicGroupKeyPrefix = &prefix
	_, groups, err := buildTargetResolver(cfg, client, nil, 1<<20)
	if err != nil || groups == nil {
		t.Fatalf("group store = %v, %v", groups, err)
	}
	if got := groups.MaxAge(); got != 10*time.Minute {
		t.Fatalf("the deployed group store serves snapshots up to %s old, want 10m", got)
	}
}
