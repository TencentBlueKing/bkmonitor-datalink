// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_getMaxVersion(t *testing.T) {
	type args struct {
		defaultVersion string
		versionList    []string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{name: "0.32.1231", args: args{defaultVersion: "0.0.0", versionList: []string{"0.4.1.61", "0.12.980", "0.13.982", "0.14.1017", "0.30.1193", "0.32.1231"}}, want: "0.32.1231"},
		{name: "0.30.1193", args: args{defaultVersion: "0.0.0", versionList: []string{"0.12.980", "0.13.982", "0.14.1017", "0.30.1193", "0.4.1.61"}}, want: "0.30.1193"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, getMaxVersion(tt.args.defaultVersion, tt.args.versionList), "getMaxVersion(%v, %v)", tt.args.defaultVersion, tt.args.versionList)
		})
	}
}
