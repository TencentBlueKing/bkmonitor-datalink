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
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/storage"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/mocktest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// FakeDataBench
func FakeDataBench(b *testing.B) (*gomock.Controller, *gostub.Stubs) {
	log.InitTestLogger()
	ctrl := gomock.NewController(b)
	basepath := "testfile"

	dirs, err := os.ReadDir(basepath)
	if err != nil {
		panic(err)
	}

	dataMap := make(map[string]*decoder.Response)

	for _, dir := range dirs {
		data, err := os.ReadFile(basepath + "/" + dir.Name())
		if err != nil {
			panic(err)
		}
		var resp = new(decoder.Response)
		err = json.Unmarshal(data, &resp)
		if err != nil {
			continue
		}
		dataMap[dir.Name()] = resp
	}

	// 制造一个返回假数据的influxdb client
	mockClient := mocktest.NewMockClient(ctrl)
	mockClient.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, db, sql, precision, contentType string, chunked bool) (*decoder.Response, error) {
		path := sql
		result := dataMap[path]
		return result, nil
	}).AnyTimes()

	_ = influxdb.InitGlobalInstance(context.Background(), &influxdb.Params{
		Timeout: 30 * time.Second,
	}, mockClient)

	stubs := gostub.New()

	return ctrl, stubs
}

// FakeData
func FakeData(t *testing.T) (*gomock.Controller, *gostub.Stubs) {
	log.InitTestLogger()
	ctrl := gomock.NewController(t)
	basepath := "testfile"

	dirs, err := os.ReadDir(basepath)
	if err != nil {
		panic(err)
	}

	dataMap := make(map[string]*decoder.Response)

	for _, dir := range dirs {
		data, err := os.ReadFile(basepath + "/" + dir.Name())
		if err != nil {
			panic(err)
		}
		var resp *decoder.Response
		err = json.Unmarshal(data, &resp)
		if err != nil {
			continue
		}
		name := strings.Split(dir.Name(), ".")[0]
		dataMap[name] = resp
	}

	// 制造一个返回假数据的influxdb client
	mockClient := mocktest.NewMockClient(ctrl)
	mockClient.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, db, sql, precision, contentType string, chunked bool) (*decoder.Response, error) {
		md5Inst := md5.New()
		md5Inst.Write([]byte(sql))
		hashSql := fmt.Sprintf("%x", md5Inst.Sum([]byte("")))

		result, ok := dataMap[hashSql]
		if !ok {
			fmt.Println(sql)
			fmt.Println(hashSql)
			fmt.Println("____________________________________________________________")
			return &decoder.Response{Results: nil}, nil
		}
		return result, nil
	}).AnyTimes()

	_ = influxdb.InitGlobalInstance(context.Background(), &influxdb.Params{
		Timeout: 30 * time.Second,
	}, mockClient)

	// mock 假路由
	var tables = map[consul.DataID][]*consul.TableID{
		150001: {
			{
				DB:                 "2_bkmonitor_time_series_1500101",
				IsSplitMeasurement: true,
			},
		},
		150002: {
			{
				DB:          "system",
				Measurement: "disk",
			},
		},
		150003: {
			{
				DB:          "system",
				Measurement: "cpu_summary",
			},
		},
		180001: {
			{
				DB:                 "2_bkapm_metric_apm_test_have_data",
				Measurement:        "bk_apm_duration",
				IsSplitMeasurement: true,
			},
		},
		100001: {
			{
				DB:                 "test_db",
				Measurement:        "test_metric",
				IsSplitMeasurement: true,
			},
		},
	}

	stubs := gostub.New()
	stubs.Stub(&influxdb.GetTableIDsByDataID, func(dataID consul.DataID) []*consul.TableID {
		return tables[dataID]
	})

	return ctrl, stubs
}

// TestBaseUsage
func TestBaseUsage(t *testing.T) {
	ctrl, stubs := FakeData(t)
	defer ctrl.Finish()
	defer stubs.Reset()

	log.InitTestLogger()
	//var ctx context.Context = &gin.Context{}
	ctx := context.Background()
	queryInfo1 := QueryInfo{
		DB:          "system",
		Measurement: "disk",
		OffsetInfo:  OffSetInfo{
			// Limit: 500,
		},
	}
	queryInfo2 := QueryInfo{
		DB:          "",
		Measurement: "",
		DataIDList:  []consul.DataID{150002, 150003},
		OffsetInfo:  OffSetInfo{
			// Limit: 500,
		},
	}
	ctx, err := QueryInfoIntoContext(ctx, "t1", "used", &queryInfo1)
	assert.Nil(t, err)
	ctx, err = QueryInfoIntoContext(ctx, "t2", "total", &queryInfo2)
	assert.Nil(t, err)

	NewEngine(&Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		EnableNegativeOffset: true,
	})
	// sql = "rate(reference_1{t1=\"g\"}[2m]) - rate(reference_1{t1=\"g\"}[2m])"
	sql := "sum by(bk_target_ip, bk_target_cloud_id) (t1)"
	r, err := QueryRange(ctx, sql, time.Unix(1665215829, 0), time.Unix(1665220090, 0), 5*time.Minute)
	// r, err := Query(ctx, sql)
	assert.Nil(t, err)
	if err == nil {
		res := r.String()
		assert.Equal(t, "headers:[_time _value]\ntypes:[float float]\ngroup keys:[bk_target_cloud_id bk_target_ip]\ngroup values:[0 127.0.0.1]\n[1665215829000 1.31015921082368e+14]\n[1665216129000 1.31025384251392e+14]\n[1665216429000 1.3102433169408e+14]\n[1665216729000 1.31013043351552e+14]\n[1665217029000 1.31014996512768e+14]\n[1665217329000 1.3103700099072e+14]\n[1665217629000 1.3104567422976e+14]\n[1665217929000 1.31061306146816e+14]\n[1665218229000 1.31039087026176e+14]\n[1665218529000 1.3106115590144e+14]\n[1665218829000 1.3106080421888e+14]\n[1665219129000 1.31057757868032e+14]\n[1665219429000 1.31056386248704e+14]\n[1665219729000 1.3106270967808e+14]\n[1665220029000 1.3096819267584e+14]\n", res)
	}
}

// TestMakeInfluxql
func TestMakeInfluxql(t *testing.T) {
	log.InitTestLogger()

	ctrl, stubs := FakeData(t)
	defer ctrl.Finish()
	defer stubs.Reset()

	var database string
	var totalSQL string
	var limit int
	var err error
	// mock掉sql处理函数，以确认生成sql的内容
	stubs.Stub(&MakeInfluxdbQuerys, func(ctx context.Context, hints *storage.SelectHints, matchers ...*labels.Matcher) ([]influxdb.SQLInfo, error) {
		var sqlInfos []influxdb.SQLInfo
		sqlInfos, err = makeInfluxdbQuery(ctx, hints, matchers...)
		if len(sqlInfos) > 0 {
			database = sqlInfos[0].DB
			totalSQL = sqlInfos[0].SQL
			limit = sqlInfos[0].Limit
		}
		return sqlInfos, err
	})
	NewEngine(&Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	conditions := [][]ConditionField{
		{
			{
				DimensionName: "test1",
				Operator:      "=",
				Value:         []string{"3", "4"},
			},
			{
				DimensionName: "test2",
				Operator:      "=",
				Value:         []string{"5", "6"},
			},
		},
		{
			{
				DimensionName: "test3",
				Operator:      "=",
				Value:         []string{"7", "8"},
			},
			{
				DimensionName: "test4",
				Operator:      "=",
				Value:         []string{"9", "10"},
			},
		},
	}

	offset := OffSetInfo{
		Limit: 20000,
	}
	ctx := context.Background()
	assert.Nil(t, err)

	queryInfo := QueryInfo{
		DB:          "system",
		Measurement: "disk",
		OffsetInfo:  offset,
		Conditions:  conditions,
		AggregateMethodList: AggrMethods{
			{
				Name:       "avg",
				Dimensions: []string{"bk_biz_id", "bk_target_cloud_id", "bk_target_ip"},
			},
		},
	}
	ctx, err = QueryInfoIntoContext(ctx, "t1", "used", &queryInfo)
	assert.Nil(t, err)

	NewEngine(&Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	// 调用QueryRange，这样内部会调用MakeInfluxdbQuery,这里不查看结果
	_, err = QueryRange(ctx, "avg (avg_over_time(t1[1m])) by (bk_biz_id,bk_target_cloud_id,bk_target_ip)", time.Unix(1621496604, 0), time.Unix(1621496964, 0), time.Minute-time.Second)
	assert.Nil(t, err)

	// 获取inflxudbQuery信息
	assert.Equal(t, "system", database)
	assert.Equal(t, `select mean("used") as _value,time as _time from "disk" where (((test1='3' or test1='4') and (test2='5' or test2='6')) or ((test3='7' or test3='8') and (test4='9' or test4='10'))) and time >= 1621496544000000000 and time < 1621496963999000000 group by "bk_biz_id","bk_target_cloud_id","bk_target_ip",time(1m0s)`, totalSQL)
	assert.Equal(t, 20000, limit)
}

// 测试不进行ctx及metric格式特殊处理，直接输入promql得到的influxdb语句
func TestPivotTableQuery(t *testing.T) {
	log.InitTestLogger()

	ctrl, stubs := FakeData(t)
	defer ctrl.Finish()
	defer stubs.Reset()

	var database string
	var totalSQL string
	var limit int
	var err error
	// mock掉sql处理函数，以确认生成sql的内容
	stubs.Stub(&MakeInfluxdbQuerys, func(ctx context.Context, hints *storage.SelectHints, matchers ...*labels.Matcher) ([]influxdb.SQLInfo, error) {
		var sqlInfos []influxdb.SQLInfo
		sqlInfos, err = makeInfluxdbQuery(ctx, hints, matchers...)
		if len(sqlInfos) > 0 {
			database = sqlInfos[0].DB
			totalSQL = sqlInfos[0].SQL
			limit = sqlInfos[0].Limit
		}
		return sqlInfos, err
	})
	NewEngine(&Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	conditions := [][]ConditionField{
		{
			{
				DimensionName: "test1",
				Operator:      "=",
				Value:         []string{"3", "4"},
			},
			{
				DimensionName: "test2",
				Operator:      "=",
				Value:         []string{"5", "6"},
			},
		},
		{
			{
				DimensionName: "test3",
				Operator:      "=",
				Value:         []string{"7", "8"},
			},
			{
				DimensionName: "test4",
				Operator:      "=",
				Value:         []string{"9", "10"},
			},
		},
	}

	offset := OffSetInfo{
		Limit: 20000,
	}
	ctx := context.Background()
	assert.Nil(t, err)

	queryInfo := QueryInfo{
		DB:           "system",
		Measurement:  "disk",
		OffsetInfo:   offset,
		Conditions:   conditions,
		IsPivotTable: true,
		AggregateMethodList: AggrMethods{
			{
				Name:       "avg",
				Dimensions: []string{"bk_biz_id", "bk_target_cloud_id", "bk_target_ip"},
			},
		},
	}
	ctx, err = QueryInfoIntoContext(ctx, "t1", "used", &queryInfo)
	assert.Nil(t, err)

	NewEngine(&Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	// 调用QueryRange，这样内部会调用MakeInfluxdbQuery,这里不查看结果
	_, err = QueryRange(ctx, "avg by(bk_biz_id,bk_target_cloud_id,bk_target_ip) (avg_over_time(t1[1m]))", time.Unix(1621496604, 0), time.Unix(1621496964, 0), time.Minute-time.Second)
	assert.Nil(t, err)

	// 获取inflxudbQuery信息
	assert.Equal(t, "system", database)
	assert.Equal(t, `select mean("metric_value") as _value,time as _time from "disk" where metric_name = 'used' and (((test1='3' or test1='4') and (test2='5' or test2='6')) or ((test3='7' or test3='8') and (test4='9' or test4='10'))) and time >= 1621496544000000000 and time < 1621496963999000000 group by "bk_biz_id","bk_target_cloud_id","bk_target_ip",time(1m0s)`, totalSQL)
	assert.Equal(t, 20000, limit)
}

// 测试condition的基础输出效果
func TestHandleCondition(t *testing.T) {

	conditions := [][]ConditionField{
		{
			{
				DimensionName: "test1",
				Operator:      "=",
				Value:         []string{"3", "4"},
			},
			{
				DimensionName: "test2",
				Operator:      "=",
				Value:         []string{"5", "6"},
			},
		},
		{
			{
				DimensionName: "test3",
				Operator:      "=",
				Value:         []string{"7", "8"},
			},
			{
				DimensionName: "test4",
				Operator:      "!=",
				Value:         []string{"9", "10"},
			},
		},
	}
	assert.Equal(
		t,
		`(((test1='3' or test1='4') and (test2='5' or test2='6')) or ((test3='7' or test3='8') and (test4!='9' and test4!='10')))`,
		MakeOrExpression(conditions, false))
}

// TestAstResult
func TestAstResult(t *testing.T) {
	//result, _ := parser.ParseExpr("quantile(0.9, rate(bkmonitorv3:db1:table1:value{t1=\"g\"}[2m])) != 0")
	//result, err := parser.ParseExpr("method_code:http_errors:rate5m{code=\"500\"} / ignoring(code) method:http_requests:rate5m \n")
	_, err := parser.ParseExpr("avg by(instance) (avg_over_time(influxdb_proxy_backend_alive_status[1m]))")
	assert.Nil(t, err)
	//r := parser.Children(result)
	//a := fmt.Sprintf("%s %#v", result.String(), r)
	//fmt.Println(a)
}

// TestAstBaseUsage
func TestAstBaseUsage(t *testing.T) {
	result := &parser.BinaryExpr{
		RHS: &parser.NumberLiteral{Val: 0.7},
		LHS: &parser.NumberLiteral{Val: 10},
		Op:  parser.ADD,
	}
	assert.Equal(t, "10 + 0.7", result.String())
}

// BenchmarkBaseUsage
func BenchmarkBaseUsage(b *testing.B) {
	ctrl, stubs := FakeDataBench(b)
	defer ctrl.Finish()
	defer stubs.Reset()

	NewEngine(&Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	log.InitTestLogger()
	ctx := context.Background()
	queryInfo1 := QueryInfo{
		DB:          "system",
		Measurement: "disk",
		// Dimensions:  []string{"bk_target_ip", "bk_target_cloud_id"},
		OffsetInfo: OffSetInfo{
			// Limit: 500,
		},
	}
	queryInfo2 := QueryInfo{
		DB:          "system",
		Measurement: "disk",
		OffsetInfo:  OffSetInfo{
			// Limit: 500,
		},
	}
	var err error
	ctx, err = QueryInfoIntoContext(ctx, "t1", "used", &queryInfo1)
	assert.Nil(b, err)
	ctx, err = QueryInfoIntoContext(ctx, "t2", "total", &queryInfo2)
	assert.Nil(b, err)

	// sql = "rate(reference_1{t1=\"g\"}[2m]) - rate(reference_1{t1=\"g\"}[2m])"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sql := "avg by(bk_target_ip, bk_target_cloud_id) (t1)"
		_, err := QueryRange(ctx, sql, time.Unix(1665215529, 0), time.Unix(1665220000, 0), time.Minute-time.Second)
		assert.Nil(b, err)
	}
}
