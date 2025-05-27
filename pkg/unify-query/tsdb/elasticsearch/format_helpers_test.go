// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatFactory_determineEffectiveNestedPath(t *testing.T) {
	tests := []struct {
		name         string
		mapping      map[string]string
		field        string
		expectedPath string
	}{
		{
			name: "field is part of a nested path but not declared nested itself",
			mapping: map[string]string{
				"events": Nested,
			},
			field:        "events.name",
			expectedPath: "events",
		},
		{
			name: "direct child of nested",
			mapping: map[string]string{
				"parent":       Nested,
				"parent.child": "keyword", // child itself is not nested type
			},
			field:        "parent.child",
			expectedPath: "parent",
		},
		{
			name: "grandchild of nested, intermediate is also nested",
			mapping: map[string]string{
				"grandparent":              Nested,
				"grandparent.parent":       Nested, // This makes 'grandparent.parent' the effective path
				"grandparent.parent.child": "keyword",
			},
			field:        "grandparent.parent.child",
			expectedPath: "grandparent.parent",
		},
		{
			name: "grandchild of nested, intermediate is NOT nested",
			mapping: map[string]string{
				"grandparent":              Nested,
				"grandparent.parent":       "object", // Intermediate path is not 'nested' type
				"grandparent.parent.child": "keyword",
			},
			field:        "grandparent.parent.child",
			expectedPath: "grandparent", // Should return the longest valid nested path prefix
		},
		{
			name: "no nested path in field",
			mapping: map[string]string{
				"name": "keyword",
			},
			field:        "name",
			expectedPath: "",
		},
		{
			name: "field is root, no mapping for it, other nested exists",
			mapping: map[string]string{
				"other.nested.path": Nested,
			},
			field:        "rootField",
			expectedPath: "",
		},
		{
			name:         "empty field string",
			mapping:      map[string]string{"a": Nested},
			field:        "",
			expectedPath: "",
		},
		{
			name:         "empty mapping",
			mapping:      map[string]string{},
			field:        "a.b.c",
			expectedPath: "",
		},
		{
			name: "field itself is declared nested",
			mapping: map[string]string{
				"events": Nested,
			},
			field:        "events", // The field itself is a path segment that is nested
			expectedPath: "",       // Current logic: finds parent nested path. If field itself is nested, it means its children are in it.
		},
		{
			name: "multi-level nested path, field is a non-nested leaf",
			mapping: map[string]string{
				"level1":             Nested,
				"level1.level2":      Nested,
				"level1.level2.leaf": "keyword",
			},
			field:        "level1.level2.leaf.nonexistent", // Even if path doesn't fully exist in mapping for leaf
			expectedPath: "level1.level2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &FormatFactory{
				ctx:     context.Background(),
				mapping: tt.mapping,
			}
			path := f.determineEffectiveNestedPath(tt.field)
			assert.Equal(t, tt.expectedPath, path)
		})
	}
}

func TestFormatFactory_applyPathTransitions(t *testing.T) {
	tests := []struct {
		name                string
		initialLogicalPath  string
		targetPath          string
		initialAggInfoList  aggInfoList // To check if it appends correctly
		expectedAggInfoList aggInfoList
		expectedLogicalPath string
	}{
		{
			name:                "root to nested1",
			initialLogicalPath:  "",
			targetPath:          "nested1",
			initialAggInfoList:  aggInfoList{},
			expectedAggInfoList: aggInfoList{NestedAgg{Name: "nested1"}},
			expectedLogicalPath: "nested1",
		},
		{
			name:                "nested1 to root",
			initialLogicalPath:  "nested1",
			targetPath:          "",
			initialAggInfoList:  aggInfoList{},
			expectedAggInfoList: aggInfoList{ReverseNestedAgg{}},
			expectedLogicalPath: "",
		},
		{
			name:                "nested1.child to nested1 (parent)",
			initialLogicalPath:  "nested1.child",
			targetPath:          "nested1",
			initialAggInfoList:  aggInfoList{},
			expectedAggInfoList: aggInfoList{ReverseNestedAgg{}},
			expectedLogicalPath: "nested1",
		},
		{
			name:                "nested1 to nested1.child (child)",
			initialLogicalPath:  "nested1",
			targetPath:          "nested1.child",
			initialAggInfoList:  aggInfoList{},
			expectedAggInfoList: aggInfoList{NestedAgg{Name: "nested1.child"}},
			expectedLogicalPath: "nested1.child",
		},
		{
			name:                "nestedA to nestedB (sibling nested)",
			initialLogicalPath:  "nestedA",
			targetPath:          "nestedB",
			initialAggInfoList:  aggInfoList{},
			expectedAggInfoList: aggInfoList{ReverseNestedAgg{}, NestedAgg{Name: "nestedB"}},
			expectedLogicalPath: "nestedB",
		},
		{
			name:               "a.b.c to a.d (common ancestor 'a')",
			initialLogicalPath: "a.b.c",
			targetPath:         "a.d",
			initialAggInfoList: aggInfoList{},
			expectedAggInfoList: aggInfoList{
				ReverseNestedAgg{},     // out of c
				ReverseNestedAgg{},     // out of b
				NestedAgg{Name: "a.d"}, // into d (path from common ancestor 'a')
			},
			expectedLogicalPath: "a.d",
		},
		{
			name:                "no transition needed (same path)",
			initialLogicalPath:  "path.A",
			targetPath:          "path.A",
			initialAggInfoList:  aggInfoList{},
			expectedAggInfoList: aggInfoList{}, // Empty, no changes
			expectedLogicalPath: "path.A",
		},
		{
			name:               "from root to deeper nested path a.b.c",
			initialLogicalPath: "",
			targetPath:         "a.b.c",
			initialAggInfoList: aggInfoList{},
			expectedAggInfoList: aggInfoList{
				NestedAgg{Name: "a"},
				NestedAgg{Name: "a.b"},
				NestedAgg{Name: "a.b.c"},
			},
			expectedLogicalPath: "a.b.c",
		},
		{
			name:               "from deeper nested path a.b.c to root",
			initialLogicalPath: "a.b.c",
			targetPath:         "",
			initialAggInfoList: aggInfoList{},
			expectedAggInfoList: aggInfoList{
				ReverseNestedAgg{}, // out of c
				ReverseNestedAgg{}, // out of b
				ReverseNestedAgg{}, // out of a
			},
			expectedLogicalPath: "",
		},
		{
			name:                "append to existing aggInfoList",
			initialLogicalPath:  "",
			targetPath:          "new.path",
			initialAggInfoList:  aggInfoList{ValueAgg{Name: "test"}},                              // Pre-existing item
			expectedAggInfoList: aggInfoList{NestedAgg{Name: "new"}, NestedAgg{Name: "new.path"}}, // Only new items
			expectedLogicalPath: "new.path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy of initialAggInfoList to avoid modification across tests if f.aggInfoList shares the slice
			testInitialAggList := make(aggInfoList, len(tt.initialAggInfoList))
			copy(testInitialAggList, tt.initialAggInfoList)

			f := &FormatFactory{
				ctx:                context.Background(),
				currentLogicalPath: tt.initialLogicalPath,
				aggInfoList:        testInitialAggList,
			}
			f.applyPathTransitions(tt.targetPath)

			// Check only the appended part of aggInfoList
			appendedAggs := f.aggInfoList[len(tt.initialAggInfoList):]
			assert.Equal(t, tt.expectedAggInfoList, appendedAggs, "appended aggInfoList mismatch")
			assert.Equal(t, tt.expectedLogicalPath, f.currentLogicalPath, "currentLogicalPath mismatch")
		})
	}
}
