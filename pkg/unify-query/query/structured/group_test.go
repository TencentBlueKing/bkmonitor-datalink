// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeDimensions(t *testing.T) {
	type args struct {
		original      []string
		addDimensions []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "merge_with_duplicates",
			args: args{
				original:      []string{"a", "b", "c"},
				addDimensions: []string{"b", "c", "d"},
			},
			want: []string{"a", "b", "c", "d"},
		},
		{
			name: "empty_original_with_add_dimensions",
			args: args{
				original:      []string{},
				addDimensions: []string{"x", "y"},
			},
			want: []string{"x", "y"},
		},
		{
			name: "non_empty_original_with_empty_add_dimensions",
			args: args{
				original:      []string{"a", "b"},
				addDimensions: []string{},
			},
			want: []string{"a", "b"},
		},
		{
			name: "both_lists_empty",
			args: args{
				original:      []string{},
				addDimensions: []string{},
			},
			want: []string{},
		},
		{
			name: "no_overlapping_elements",
			args: args{
				original:      []string{"a", "b"},
				addDimensions: []string{"c", "d"},
			},
			want: []string{"a", "b", "c", "d"},
		},
		{
			name: "all_elements_in_add_dimensions",
			args: args{
				original:      []string{"a", "b"},
				addDimensions: []string{"a", "b", "c"},
			},
			want: []string{"a", "b", "c"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, MergeDimensions(tt.args.original, tt.args.addDimensions), "MergeDimensions(%v, %v)", tt.args.original, tt.args.addDimensions)
		})
	}
}
