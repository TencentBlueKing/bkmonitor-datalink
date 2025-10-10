// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// TestParseSQL
func TestParseSQL(t *testing.T) {
	log.InitTestLogger()
	data := []struct {
		sql    string
		hasErr bool
	}{
		{
			sql:    "select * from test",
			hasErr: false,
		},
		{
			sql:    "select * from test;drop database db1",
			hasErr: true,
		},
		{
			sql:    "select * from test where a='1';drop database db1",
			hasErr: true,
		},
	}

	for _, item := range data {
		err := influxdb.CheckSQLInject(context.Background(), item.sql)
		if item.hasErr {
			assert.NotNil(t, err)
		} else {
			assert.Nil(t, err)
		}
	}
}
