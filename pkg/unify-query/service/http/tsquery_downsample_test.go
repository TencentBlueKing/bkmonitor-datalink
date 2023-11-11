// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/influxdata/influxql"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/mocktest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
)

// mockDownSampleData
func mockDownSampleData(t *testing.T) (*gomock.Controller, *gostub.Stubs) {
	log.InitTestLogger()
	ctrl := gomock.NewController(t)
	baseT := time.Unix(0, 0)

	originRes := []decoder.Result{
		{
			Series: []*decoder.Row{
				{
					Name:    "result_0",
					Columns: []string{"_time", "_value", "ip"},
					Values: [][]interface{}{
						{baseT.Format(time.RFC3339Nano), int64(10), "127.0.0.1"}, {baseT.Add(59 * time.Second).Format(time.RFC3339Nano), int64(20), "127.0.0.1"},
						{baseT.Add(119 * time.Second).Format(time.RFC3339Nano), int64(20), "127.0.0.1"}, {baseT.Add(179 * time.Second).Format(time.RFC3339Nano), int64(20), "127.0.0.1"},
						{baseT.Add(239 * time.Second).Format(time.RFC3339Nano), int64(30), "127.0.0.1"}, {baseT.Add(299 * time.Second).Format(time.RFC3339Nano), int64(30), "127.0.0.1"},
					},
				},
			},
		},
	}

	results := map[string]decoder.Response{
		"metric": {
			Results: originRes,
			Err:     "",
		},
		"mean_ip, time(2m)": {
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name:    "result_0",
							Tags:    map[string]string{"ip": "127.0.0.1"},
							Columns: []string{"_time", "_value"},
							Values: [][]interface{}{
								{baseT.Format(time.RFC3339Nano), 16.66},
								{baseT.Add(120 * time.Second).Format(time.RFC3339Nano), float64(25)},
								{baseT.Add(240 * time.Second).Format(time.RFC3339Nano), float64(30)},
							},
						},
					},
				},
			},
			Err: "",
		},
		"mean_time(1m)": {
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name:    "result_0",
							Tags:    map[string]string{"ip": "127.0.0.1"},
							Columns: []string{"_time", "_value"},
							Values: [][]interface{}{
								{baseT.Format(time.RFC3339Nano), int64(15)}, {baseT.Add(60 * time.Second).Format(time.RFC3339Nano), int64(20)},
								{baseT.Add(120 * time.Second).Format(time.RFC3339Nano), int64(20)}, {baseT.Add(180 * time.Second).Format(time.RFC3339Nano), int64(30)},
								{baseT.Add(240 * time.Second).Format(time.RFC3339Nano), int64(30)},
							},
						},
					},
				},
			},
			Err: "",
		},
		"sum_time(2m)": {
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name:    "result_0",
							Tags:    map[string]string{"ip": "127.0.0.1"},
							Columns: []string{"_time", "_value"},
							Values: [][]interface{}{
								{baseT.Format(time.RFC3339Nano), int64(50)},
								{baseT.Add(120 * time.Second).Format(time.RFC3339Nano), int64(50)},
								{baseT.Add(240 * time.Second).Format(time.RFC3339Nano), int64(30)},
							},
						},
					},
				},
			},
			Err: "",
		},
		"sum_time(1m)": {
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name:    "result_0",
							Tags:    map[string]string{"ip": "127.0.0.1"},
							Columns: []string{"_time", "_value"},
							Values: [][]interface{}{
								{baseT.Format(time.RFC3339Nano), int64(30)}, {baseT.Add(60 * time.Second).Format(time.RFC3339Nano), int64(20)},
								{baseT.Add(120 * time.Second).Format(time.RFC3339Nano), int64(20)}, {baseT.Add(180 * time.Second).Format(time.RFC3339Nano), int64(30)},
								{baseT.Add(240 * time.Second).Format(time.RFC3339Nano), int64(30)},
							},
						},
					},
				},
			},
			Err: "",
		},
		"max_ip, time(1m)": {
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name:    "result_0",
							Columns: []string{"_time", "_value", "ip"},
							Tags:    map[string]string{"ip": "127.0.0.1"},
							Values: [][]interface{}{
								{baseT.Format(time.RFC3339Nano), int64(10), "127.0.0.1"},
								{baseT.Add(59 * time.Second).Format(time.RFC3339Nano), int64(20), "127.0.0.1"},
								{baseT.Add(119 * time.Second).Format(time.RFC3339Nano), int64(20), "127.0.0.1"},
								{baseT.Add(179 * time.Second).Format(time.RFC3339Nano), int64(20), "127.0.0.1"},
								{baseT.Add(239 * time.Second).Format(time.RFC3339Nano), int64(30), "127.0.0.1"},
								{baseT.Add(299 * time.Second).Format(time.RFC3339Nano), int64(30), "127.0.0.1"},
							},
						},
					},
				},
			},
			Err: "",
		},
		"last_*, time(2m)": {
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name:    "result_0",
							Tags:    map[string]string{"ip": "127.0.0.1"},
							Columns: []string{"_time", "_value"},
							Values: [][]interface{}{
								{baseT.Format(time.RFC3339Nano), int64(10)},
								{baseT.Add(60 * time.Second).Format(time.RFC3339Nano), int64(10)},
								{baseT.Add(120 * time.Second).Format(time.RFC3339Nano), int64(10)},
								{baseT.Add(180 * time.Second).Format(time.RFC3339Nano), int64(10)},
								{baseT.Add(240 * time.Second).Format(time.RFC3339Nano), int64(10)},
							},
						},
					},
				},
			},
			Err: "",
		},
	}

	mockClient := mocktest.NewMockClient(ctrl)
	mockClient.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, db, sql, precision, contentType string, chunked bool) (*decoder.Response, error) {
		statement := influxql.MustParseStatement(sql).(*influxql.SelectStatement)
		fmt.Println(statement.Fields)
		var metric string
		switch e := statement.Fields[0].Expr.(type) {
		case *influxql.VarRef:
			metric = e.Val
		case *influxql.Call:
			metric = e.Name + "_" + statement.Dimensions.String()
		}

		result, ok := results[metric]
		if !ok {
			return nil, fmt.Errorf("tsquery_downsample_test.go metric: %s is no data", metric)
		}
		return &result, nil
	}).AnyTimes()

	stubs := gostub.New()
	_ = influxdb.InitGlobalInstance(context.Background(), &influxdb.Params{
		Timeout: 30 * time.Second,
	}, mockClient)

	return ctrl, stubs
}

// TestDownSample
func TestDownSample(t *testing.T) {
	// 测试降采样
	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})
	// 初始化日志等环境信息
	// mock假数据
	ctrl, stubs := mockDownSampleData(t)
	defer ctrl.Finish()
	defer stubs.Reset()

	testCases := map[string]struct {
		req    string
		reqUrl string
		expect *PromData
		err    error
	}{
		"origin": {
			req:    `{"promql":"bkmonitor:db:m:metric","start":"0","end":"300","step":"60s"}`,
			reqUrl: "query/ts/promql",
			expect: &PromData{
				dimensions: map[string]bool{},
				Tables: []*TablesItem{
					{
						Name:        "_result0",
						Columns:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"ip"},
						GroupValues: []string{"127.0.0.1"},
						Values: [][]interface{}{
							{int64(0), float64(10)},
							{int64(60000), float64(20)},
							{int64(120000), float64(20)},
							{int64(180000), float64(20)},
							{int64(240000), float64(30)},
						},
					},
				},
			},
		},
		"avg aggr": {
			req: `{"promql":"avg(bkmonitor:db:m:metric)","start":"0","end":"300","step":"2m"}`,
			expect: &PromData{
				dimensions: map[string]bool{},
				Tables: []*TablesItem{
					{
						Name:        "_result0",
						Columns:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{},
						GroupValues: []string{},
						Values: [][]interface{}{
							{int64(0), float64(10)},
							{int64(120000), float64(10)},
							{int64(240000), float64(10)},
						},
					},
				},
			},
		},
		"avg by": {
			req: `{"promql":"avg(bkmonitor:db:m:metric) by (ip)","start":"0","end":"300","step":"60s"}`,
			expect: &PromData{
				dimensions: map[string]bool{},
				Tables: []*TablesItem{
					{
						Name:        "_result0",
						Columns:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"ip"},
						GroupValues: []string{"127.0.0.1"},
						Values: [][]interface{}{
							// 预先聚合
							{int64(0), float64(10)},      // [0, 1m]
							{int64(60000), float64(20)},  // [1m, 2m] 没有点，寻找前面的点
							{int64(120000), float64(20)}, // [2m, 3m]
							{int64(180000), float64(20)}, // [3m, 4m]
							{int64(240000), float64(30)}, // [4m, 5m]
						},
					},
				},
			},
		},
		// step != range 的时候，数据会出现偏差
		"avg by and avg_over_time": {
			req: `{"promql":"avg(avg_over_time(bkmonitor:db:m:metric[2m])) by (ip)","start":"0","end":"300","step":"1m"}`,
			expect: &PromData{
				dimensions: map[string]bool{},
				Tables: []*TablesItem{
					{
						Name:        "_result0",
						Columns:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"ip"},
						GroupValues: []string{"127.0.0.1"},
						Values: [][]interface{}{
							{int64(0), 16.66},            // [-2m,0s] -> [-1m1ms, 59s999ms]
							{int64(60000), 16.66},        // [-1ms, 1m59s999ms]
							{int64(120000), float64(25)}, // [59s999ms, 2m59s999ms]
							{int64(180000), float64(25)}, // [1m59s999ms, 3m59s999ms]
							{int64(240000), float64(30)}, // [2m59s999ms, 4m59s999ms]
						},
					},
				},
			},
		},
		// 没有_over_time ，则默认为step=range
		"sum aggr": {
			req: `{"promql":"sum(bkmonitor:db:m:metric)","start":"0","end":"300","step":"2m"}`,
			expect: &PromData{
				dimensions: map[string]bool{},
				Tables: []*TablesItem{
					{
						Name:        "_result0",
						Columns:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{},
						GroupValues: []string{},
						Values: [][]interface{}{
							// 没有维度，直接返回原始数据，找离step最近的点
							{int64(0), float64(10)},
							{int64(120000), float64(10)},
							{int64(240000), float64(10)},
						},
					},
				},
			},
		},
		// 普罗引擎里面 使用 sum_over_time 函数时，当 range != 真实数据点的周期(采集周期)，就会出现与 sum() 函数结果不同的情况。（其他函数也会，但是可能没这么明显）
		// 这里底层用influxdb同样会出现这样的问题，但是由于sum_over_time的聚合时间是用户传的，这部分数据不准确的风险要用户自己承担。
		// 且这里是为了提前聚合维度来减少流量，故保留sum的降采样聚合方式
		"max by and max_over_time": {
			req: `{"promql":"max(max_over_time(bkmonitor:db:m:metric[1m])) by (ip)","start":"0","end":"300","step":"1m"}`,
			expect: &PromData{
				dimensions: map[string]bool{},
				Tables: []*TablesItem{
					{
						Name:        "_result0",
						Columns:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"ip"},
						GroupValues: []string{"127.0.0.1"},
						Values: [][]interface{}{
							{int64(0), float64(20)},
							{int64(60000), float64(20)},
							{int64(120000), float64(20)},
							{int64(180000), float64(30)},
							{int64(240000), float64(30)},
						},
					},
				},
			},
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx := mock.Init(context.Background())
			// 根据请求参数请求url，使用mock的假数据看数据的计算结果
			res, err := handlePromqlQuery(ctx, testCase.req, nil, "")
			if testCase.err != nil {
				assert.Equal(t, err, testCase.err, name)
			} else {
				assert.NoError(t, err, name)
			}
			log.Infof(ctx, "%v", res)
			assert.Equal(t, testCase.expect, res, name)
		})
	}
}
