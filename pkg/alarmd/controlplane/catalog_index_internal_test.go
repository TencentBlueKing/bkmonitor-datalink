// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"testing"
)

func TestIndexFromCatalogRejectsEmptyAndDuplicateQueryGroups(t *testing.T) {
	if _, err := indexFromCatalog(Catalog{QueryGroups: []QueryGroup{{Identity: ""}}}); err == nil {
		t.Fatal("an empty Query Group was indexed")
	}
	if _, err := indexFromCatalog(Catalog{QueryGroups: []QueryGroup{{Identity: "a"}, {Identity: "a"}}}); err == nil {
		t.Fatal("a duplicate Query Group was indexed")
	}
}
