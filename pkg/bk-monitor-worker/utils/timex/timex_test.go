// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package timex

import (
	"testing"
	"time"
)

func TestParsePyDateFormat(t *testing.T) {
	type args struct {
		dataFormat string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{name: testing.CoverMode(), args: args{"%Y%m%d"}, want: "20060102"},
		{name: testing.CoverMode(), args: args{"%y%m%d"}, want: "060102"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParsePyDateFormat(tt.args.dataFormat); got != tt.want {
				t.Errorf("ParsePyDateFormat() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	type args struct {
		s string
	}
	tests := []struct {
		name    string
		args    args
		want    time.Duration
		wantErr bool
	}{
		{name: "1s", args: args{"1s"}, want: time.Second, wantErr: false},
		{name: "1h", args: args{"1h"}, want: time.Hour, wantErr: false},
		{name: "1d", args: args{"1d"}, want: 24 * time.Hour, wantErr: false},
		{name: "1w", args: args{"1w"}, want: 7 * 24 * time.Hour, wantErr: false},
		{name: "1d2w", args: args{"1d2w"}, want: 15 * 24 * time.Hour, wantErr: false},
		{name: "1d2h", args: args{"1d2h"}, want: 26 * time.Hour, wantErr: false},
		{name: "1d2h3y", args: args{"1d2h3y"}, want: 0, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDuration(tt.args.s)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseDuration() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseDuration() got = %v, want %v", got, tt.want)
			}
		})
	}
}
