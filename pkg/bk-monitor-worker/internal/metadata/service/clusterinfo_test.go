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
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/stretchr/testify/assert"
)

func TestClusterInfoSvc_base64WithPrefix(t *testing.T) {
	type fields struct {
		ClusterInfo *storage.ClusterInfo
	}
	type args struct {
		content string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   func(s string) bool
	}{
		{name: "no_content", fields: fields{}, args: args{content: ""}, want: func(s string) bool { return s == "" }},
		{name: "base64", fields: fields{}, args: args{content: "abc"}, want: func(s string) bool { return strings.HasPrefix(s, "base64://") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := ClusterInfoSvc{
				ClusterInfo: tt.fields.ClusterInfo,
			}
			assert.Truef(t, tt.want(k.base64WithPrefix(tt.args.content)), "base64WithPrefix(%v)", tt.args.content)
		})
	}
}
