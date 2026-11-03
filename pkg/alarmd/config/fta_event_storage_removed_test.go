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

// FTA event sources are not supported, and no setting brings them back: the
// key that once routed them is gone, and the strict decoder refuses a
// configuration that still names it rather than ignoring it.
func TestAConfigurationNamingTheFTAEventStorageIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	document := "phase_two:\n  control:\n    legacy_query_runtime:\n      fta_event_storage:\n        table_id: fta.events\n"
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	requireIgnored(t, unknownKeysOf(t, string(raw)), "legacy_query_runtime")
}
