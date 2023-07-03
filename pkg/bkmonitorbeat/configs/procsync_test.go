// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnsureProcsyncHash(t *testing.T) {
	type args struct {
		i int32
	}
	tests := []struct {
		name string
		args args
		want int32
	}{
		{
			"smaller",
			args{
				minProcsyncHash - 100,
			},
			minProcsyncHash*2 - 100,
		},
		{
			"bigger",
			args{
				minProcsyncHash + 100,
			},
			minProcsyncHash + 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, EnsureProcsyncHash(tt.args.i), "EnsureProcsyncHash(%v)", tt.args.i)
		})
	}
}

func TestIsProcsyncHash(t *testing.T) {
	type args struct {
		i int32
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			"smaller",
			args{
				minProcsyncHash - 100,
			},
			false,
		},
		{
			"bigger",
			args{
				minProcsyncHash + 100,
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, IsProcsyncHash(tt.args.i), "IsProcsyncHash(%v)", tt.args.i)
		})
	}
}
