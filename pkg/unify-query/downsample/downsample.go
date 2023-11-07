// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package downsample

import (
	"context"
	"math"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// Downsample 数据降采样
func Downsample(points []promql.Point, factor float64) []promql.Point {
	var threshold int
	var downSamplePoints []promql.Point
	// threshold 最大值
	threshold = int(math.Ceil(float64(len(points)) * factor))
	downSamplePoints = lttbFunc(points, threshold)

	log.Debugf(context.TODO(), "downsample series done %s %d %s %d %s %d",
		"threshold", threshold,
		"rawPointCount", len(points),
		"downsamplePointCount", len(downSamplePoints),
	)
	return downSamplePoints
}

// CheckDownSampleRange 检查降采样周期
func CheckDownSampleRange(step, downSampleRange string) (bool, float64, error) {
	var stepTime time.Duration
	var downSampleRangeTime time.Duration
	var dTmp model.Duration
	var err error
	dTmp, err = model.ParseDuration(step)
	if err != nil {
		return false, 0, err
	}
	stepTime = time.Duration(dTmp)
	dTmp, err = model.ParseDuration(downSampleRange)
	if err != nil {
		return false, 0, err
	}
	downSampleRangeTime = time.Duration(dTmp)

	return downSampleRangeTime > stepTime,
		float64(stepTime.Milliseconds()) / float64(downSampleRangeTime.Milliseconds()),
		err
}
