// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"fmt"
	"time"

	"github.com/prometheus/common/model"
)

const (
	Level0RetentionPolicy = "" // 默认RP策略，InfluxDB自动生成
	Level2RetentionPolicy = "5m"
	Level3RetentionPolicy = "1h"
	Level4RetentionPolicy = "12h"
)

// DefaultRetentionPolicies 默认RP列表
var DefaultRetentionPolicies = []RetentionPolicy{
	NewRetentionPolicy(Level0RetentionPolicy),
	// NewRetentionPolicy(Level1RetentionPolicy),
	NewRetentionPolicy(Level2RetentionPolicy),
	NewRetentionPolicy(Level3RetentionPolicy),
	NewRetentionPolicy(Level4RetentionPolicy),
}

// RetentionPolicy RP接口
type RetentionPolicy interface {
	String() string
	IsDownSampled() bool
	Time() time.Duration
	GetFieldNames(field, aggr string) ([]string, string)
}

// retentionPolicy
type retentionPolicy struct {
	Name string
}

// NewRetentionPolicy
func NewRetentionPolicy(name string) RetentionPolicy {
	return &retentionPolicy{
		Name: name,
	}
}

// String
func (rp *retentionPolicy) String() string {
	return rp.Name
}

// IsDownSampled
func (rp *retentionPolicy) IsDownSampled() bool {
	return rp.String() != Level0RetentionPolicy
}

// Time 获取该RP的降精度周期
func (rp *retentionPolicy) Time() time.Duration {
	t, err := model.ParseDuration(rp.String())
	if err != nil {
		return time.Duration(0)
	}
	return time.Duration(t)
}

// fields 根据聚合函数生成对应指标
func (rp *retentionPolicy) GetFieldNames(field, aggr string) ([]string, string) {
	var fields []string

	if rp.IsDownSampled() {
		if aggr == MIN {
			fields = append(fields, fmt.Sprintf("\"%s_%s\"", MIN, field))
			return fields, MIN
		} else if aggr == MAX {
			fields = append(fields, fmt.Sprintf("\"%s_%s\"", MAX, field))
			return fields, MAX
		} else if aggr == SUM {
			fields = append(fields, fmt.Sprintf("\"%s_%s\"", SUM, field))
			return fields, SUM
		} else if aggr == COUNT {
			fields = append(fields, fmt.Sprintf("\"%s_%s\"", COUNT, field))
			return fields, SUM
		} else if aggr == LAST {
			fields = append(fields, fmt.Sprintf("\"%s_%s\"", LAST, field))
			return fields, LAST
		} else {
			// 默认使用mean降精度
			// table 模型不支持多指标，暂时先屏蔽
			fields = append(fields, fmt.Sprintf("\"%s_%s\"", MEAN, field))
			return fields, MEAN
		}
	}

	return []string{fmt.Sprintf("\"%s\"", field)}, aggr
}
