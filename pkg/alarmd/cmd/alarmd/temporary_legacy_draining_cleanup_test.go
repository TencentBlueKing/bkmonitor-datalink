// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadTemporaryLegacyDrainingCleanupRequestIsStrict(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
	}{
		{name: "unknown field", content: `{"unknown":true}`},
		{name: "trailing value", content: `{}` + "\n" + `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "request.json")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadTemporaryLegacyDrainingCleanupRequest(path); err == nil || !strings.Contains(err.Error(), "decode") {
				t.Fatalf("loadTemporaryLegacyDrainingCleanupRequest(%s) error=%v", test.name, err)
			}
		})
	}
}
