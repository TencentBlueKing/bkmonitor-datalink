// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The production bundle's first reconcile round sweeps the Assignment
// records and writes one line about it through the production logging
// chain -- the real LoggingObserver behind the real limiter, not a test
// observer -- with the stage and the numbers. On the deployment that
// motivated this test six records were reclaimed and no line said so;
// this pins that the line reaches the log from the production wiring, so a
// missing line in production is a question about which process swept and
// not about whether the sweep can be seen.
func TestTheProductionBundleLogsTheSweepItRan(t *testing.T) {
	var output bytes.Buffer
	logger := observability.New(observability.ComponentRuntime, &output)
	var strayKey string
	fixture := startCutoverFixtureWith(t, nil, logger, func(ctx context.Context, client *redis.Client, cfg config.Config) {
		// A record of a Query Group nothing publishes, left by a retired
		// deployment, in the store's own key shape.
		retired := "retired-query-group"
		tag := sha256.Sum256([]byte(retired))
		strayKey = productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership") + ":{" + hex.EncodeToString(tag[:]) + "}:assignment"
		if err := client.HSet(ctx, strayKey, "query_group", retired, "desired_worker_id", "worker-of-a-retired-replica", "record_revision", "3").Err(); err != nil {
			t.Fatal(err)
		}
	})
	ctx := context.Background()
	if exists, err := fixture.redisClient.Exists(ctx, strayKey).Result(); err != nil || exists != 0 {
		t.Fatalf("the stray record is still there (exists=%d err=%v): the sweep did not run, so the line below would prove nothing", exists, err)
	}
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var line map[string]any
		if json.Unmarshal([]byte(raw), &line) == nil && line["stage"] == observability.StageAssignmentSwept {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("assignment_swept lines = %d, want exactly one for the term's first round (log had %d bytes)", len(lines), output.Len())
	}
	line := lines[0]
	if line["result"] != "success" || line["component"] != observability.ComponentOwnership ||
		line["assignment_sweep_scanned"] != float64(3) || line["assignment_sweep_retired"] != float64(1) ||
		line["assignment_sweep_reclaimed"] != float64(1) || line["assignment_sweep_held_by_lease"] != float64(0) {
		t.Fatalf("line = %v, want success with scanned 3 (two live records and the stray), retired 1, reclaimed 1", line)
	}
}
