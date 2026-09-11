// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"testing"

	"gorm.io/gorm"
)

func TestMySQLReadersShareOneDatabase(t *testing.T) {
	t.Parallel()
	database := &gorm.DB{}
	sources, err := newMySQLSources(database)
	if err != nil {
		t.Fatal(err)
	}
	cwStrategy := sources.CWStrategy.(*CWStrategyClient)
	alarmSource := sources.AlarmSource.(*AlarmSourceClient)
	metric := sources.Metric.(*MetricClient)
	if cwStrategy.db != database || alarmSource.db != database || metric.db != database {
		t.Fatal("mysql readers do not share the enrich database")
	}
}
