// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jsonx

import (
	"testing"
)

func TestCompareObjects(t *testing.T) {
	type args struct {
		objA any
		objB any
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{name: "nil", args: args{objA: nil, objB: nil}, want: true, wantErr: false},
		{name: "list-true", args: args{objA: []any{1, 2, 3}, objB: []any{3, 2, 1}}, want: true, wantErr: false},
		{name: "list-false", args: args{objA: []any{1, 2, 3}, objB: []any{3, 2, 1, 0}}, want: false, wantErr: false},
		{name: "map-true", args: args{objA: map[string]any{"a": "a1"}, objB: map[string]any{"a": "a1"}}, want: true, wantErr: false},
		{name: "map-false", args: args{objA: map[string]any{"a": "a1"}, objB: map[string]any{"a": "a2"}}, want: false, wantErr: false},
		{name: "map-false2", args: args{objA: map[string]any{"a": "a1"}, objB: map[string]any{"b": "a1"}}, want: false, wantErr: false},
		{name: "map-false3", args: args{objA: map[string]any{"a": "a1"}, objB: map[string]any{"b": "b1", "a": "a1"}}, want: false, wantErr: false},
		{name: "map-list-true", args: args{objA: map[string]any{"a": []string{"1", "2"}}, objB: map[string]any{"a": []string{"2", "1"}}}, want: true, wantErr: false},
		{name: "map-list-false", args: args{objA: map[string]any{"a": []string{"1", "2"}}, objB: map[string]any{"a": []string{"2", "1", "3"}}}, want: false, wantErr: false},
		{name: "map-map-true", args: args{objA: map[string]any{"a": map[string]any{"b": "2"}}, objB: map[string]any{"a": map[string]any{"b": "2"}}}, want: true, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CompareObjects(tt.args.objA, tt.args.objB)
			if (err != nil) != tt.wantErr {
				t.Errorf("CompareObjects() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("CompareObjects() got = %v, want %v", got, tt.want)
			}
		})
	}
}
