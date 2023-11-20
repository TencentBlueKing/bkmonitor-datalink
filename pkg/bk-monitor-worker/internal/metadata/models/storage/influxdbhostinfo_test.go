// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJudgeShardByDuration(t *testing.T) {
	type args struct {
		duration string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{name: "", args: args{""}, want: "7d", wantErr: false},
		{name: "10m", args: args{"10m"}, want: "", wantErr: true},
		{name: "1d", args: args{"1d"}, want: "1h", wantErr: false},
		{name: "90d", args: args{"90d"}, want: "1d", wantErr: false},
		{name: "200d", args: args{"200d"}, want: "7d", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := JudgeShardByDuration(tt.args.duration)
			if (err != nil) != tt.wantErr {
				t.Errorf("JudgeShardByDuration() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("JudgeShardByDuration() got = %v, want %v", got, tt.want)
			}
			assert.Equalf(t, tt.want, got, "JudgeShardByDuration(%v)", tt.args.duration)
		})
	}
}
