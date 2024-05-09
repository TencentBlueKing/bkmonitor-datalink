// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestInstance_queryReference(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	log.InitTestLogger()

	url := "http://127.0.0.1:9200"
	username := "elastic"
	password := ""
	timeout := time.Minute * 10
	maxRouting := 10
	maxSize := 10000

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ins, err := NewInstance(ctx, &InstanceOption{
		Url:        url,
		Username:   username,
		Password:   password,
		MaxRouting: maxRouting,
		MaxSize:    maxSize,
	})
	if err != nil {
		t.Fatal(err)
		return
	}

	defaultEnd := time.Now()
	defaultStart := defaultEnd.Add(-1 * time.Hour)

	db := "2_bklog_bkapigateway_esb_container1"
	field := "gseIndex"

	for idx, c := range []struct {
		query *metadata.Query
		start time.Time
		end   time.Time

		expected interface{}
	}{
		{
			query: &metadata.Query{
				QueryString: "",
				DB:          db,
				Field:       field,
				From:        0,
				Size:        10,
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		{
			query: &metadata.Query{
				QueryString: "",
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				TimeAggregation: &metadata.TimeAggregation{
					Function:       AvgOT,
					WindowDuration: time.Minute * 1,
				},
				AggregateMethodList: metadata.AggregateMethodList{
					{
						Name: AVG,
						Dimensions: []string{
							"__ext___io_kubernetes_pod",
							"__ext___container_name",
						},
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		{
			query: &metadata.Query{
				QueryString: "",
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				AggregateMethodList: metadata.AggregateMethodList{
					{
						Name: COUNT,
						Dimensions: []string{
							"serverIp",
						},
					},
				},
				IsNotPromQL: true,
			},
			start: defaultStart,
			end:   defaultEnd,
		},
	} {
		t.Run(fmt.Sprintf("testing run: %d", idx), func(t *testing.T) {
			refName := "a"
			reference := metadata.QueryReference{
				refName: &metadata.QueryMetric{
					QueryList: metadata.QueryList{
						c.query,
					},
					ReferenceName: refName,
				},
			}
			err = metadata.SetQueryReference(ctx, reference)
			if err != nil {
				log.Fatalf(ctx, err.Error())
				return
			}

			matrix, err := ins.QueryRange(ctx, refName, c.start, c.end, 0)
			if err != nil {
				log.Fatalf(ctx, err.Error())
				return
			}

			fmt.Printf("is promql: %vs\n", !c.query.IsNotPromQL)
			left := c.end.Unix() - c.start.Unix()
			fmt.Printf("range time: %ds\n", left)

			fmt.Println(matrix.String())

			//for _, r := range matrix {
			//	lbs := make([]string, 0)
			//	for _, lb := range r.Metric {
			//		lbs = append(lbs, fmt.Sprintf("%s=%s", lb.Name, lb.Value))
			//	}
			//
			//	fmt.Printf("name: %s\n", strings.Join(lbs, ", "))
			//	fmt.Printf("sample num: %d\n", len(r.String()))
			//	i := 0
			//	for {
			//		if len(r.GetSamples()) > i {
			//			sample := r.GetSamples()[i]
			//			fmt.Printf("sample example %d: timestamp: %d, value: %.f\n", i+1, sample.GetTimestamp(), sample.GetValue())
			//			if i >= 4 {
			//				break
			//			}
			//		} else {
			//			break
			//		}
			//		i++
			//	}
			//}
		})
	}
}
