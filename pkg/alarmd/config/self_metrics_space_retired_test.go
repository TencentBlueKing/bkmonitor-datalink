// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The space the trends read is derived from the collection's own wiring
// (bk-monitor-operator's DataIDs), so no setting names it: a configuration
// that still does is refused by the strict decoder, not silently ignored.
func TestAConfigurationNamingTheSelfMetricsSpaceIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	document := "phase_two:\n  access:\n    self_metrics_space_uid: bkcc__2\n"
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	requireIgnored(t, unknownKeysOf(t, string(raw)), "self_metrics_space_uid")
}
