// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package remote

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func TestSpaceReporter__Do(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mocker.InitTestDBConfig("../../bmw_test.yaml")

	rp, err := NewSpaceReporter(config.BuildInResultTableDetailKey, config.PromRemoteWriteUrl)
	if err != nil {
		logger.Fatal(err.Error())
	}

	metric := fmt.Sprintf("space_reporter_%s", time.Now().Format("2006010215"))
	ts := append([]prompb.TimeSeries{
		{
			Labels: []prompb.Label{
				{
					Name:  "__name__",
					Value: metric,
				},
				{
					Name:  "role",
					Value: "child",
				},
				{
					Name:  "status",
					Value: "pending",
				},
				{
					Name:  "date",
					Value: time.Now().Format("2006010215"),
				},
			},
			Samples: []prompb.Sample{
				{
					Timestamp: time.Now().UnixMilli(),
					Value:     rand.Float64() * float64(rand.Intn(100)),
				},
			},
		},
		{
			Labels: []prompb.Label{
				{
					Name:  "__name__",
					Value: metric,
				},
				{
					Name:  "role",
					Value: "child",
				},
				{
					Name:  "status",
					Value: "running",
				},
				{
					Name:  "date",
					Value: time.Now().Format("2006010215"),
				},
			},
			Samples: []prompb.Sample{
				{
					Timestamp: time.Now().UnixMilli(),
					Value:     rand.Float64() * float64(rand.Intn(100)),
				},
			},
		},
		{
			Labels: []prompb.Label{
				{
					Name:  "__name__",
					Value: metric,
				},
				{
					Name:  "role",
					Value: "parent",
				},
				{
					Name:  "status",
					Value: "pending",
				},
				{
					Name:  "date",
					Value: time.Now().Format("2006010215"),
				},
			},
			Samples: []prompb.Sample{
				{
					Timestamp: time.Now().UnixMilli(),
					Value:     rand.Float64() * float64(rand.Intn(100)),
				},
			},
		},
		{
			Labels: []prompb.Label{
				{
					Name:  "__name__",
					Value: metric,
				},
				{
					Name:  "role",
					Value: "parent",
				},
				{
					Name:  "status",
					Value: "running",
				},
				{
					Name:  "date",
					Value: time.Now().Format("2006010215"),
				},
			},
			Samples: []prompb.Sample{
				{
					Timestamp: time.Now().UnixMilli(),
					Value:     rand.Float64() * float64(rand.Intn(100)),
				},
			},
		},
	})

	err = rp.Do(ctx, "bkcc__7", ts...)
	assert.Nil(t, err)
}
