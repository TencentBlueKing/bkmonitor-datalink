// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package mysqlstore

import (
	"strings"
	"testing"
)

func TestEntityQueryLayoutMatchesEmbeddedSchema(t *testing.T) {
	schema, err := migrationFiles.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	layout := EntityQueryLayout()
	for _, table := range []string{layout.EventTable, layout.AlertTable, layout.AlertLogTable} {
		if !strings.Contains(string(schema), "CREATE TABLE IF NOT EXISTS "+table+" (") {
			t.Fatalf("advertised table %s is not owned by the current schema", table)
		}
	}
}
