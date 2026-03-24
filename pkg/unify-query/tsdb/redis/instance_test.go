// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"testing"

	"github.com/go-gota/gota/dataframe"
	"github.com/go-gota/gota/series"
)

func TestDataframe(t *testing.T) {
	df := dataframe.LoadRecords(
		[][]string{
			{"A", "B", "D"},
		},
		dataframe.DetectTypes(false),
		dataframe.DefaultType(series.String),
		dataframe.WithTypes(map[string]series.Type{
			"A": series.String,
			"D": series.Bool,
			"B": series.Float,
		}),
	)
	df2 := dataframe.LoadRecords(
		[][]string{
			{"A", "B", "D"},
			{"A", "1.1", "false"},
		},
		dataframe.DetectTypes(false),
		dataframe.DefaultType(series.String),
		dataframe.WithTypes(map[string]series.Type{
			"A": series.String,
			"D": series.Bool,
			"B": series.Int,
		}),
	)
	c := df.RBind(df2)
	t.Logf("dataframe: %v ", c)
}
