// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	tshttp "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/route"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/route/influxql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

var ch = make(chan string, 1)

type HttpServiceSuite struct {
	suite.Suite
	hs    *tshttp.Service
	stubs *gostub.Stubs
	ctrl  *gomock.Controller
}

func (suite *HttpServiceSuite) SetupSuite() {
	// 模仿配置文件
	viper.Set("batch_size", 1)
	viper.Set("authorization.enable", false)
	viper.Set("consul.address", "192.168.136.128:8500")
	viper.Set("consul.health.period", "30s")
	viper.Set("kafka.address", "192.168.136.128")
	viper.Set("kafka.port", "9092")
	viper.Set("kafka.topic_prefix", "bkmonitor")
	viper.Set("kafka.version", "0.10.2.0")

	// mock cluster
	suite.ctrl = gomock.NewController(suite.T())
	suite.stubs = gostub.New()
	// 关闭所有初始化
	suite.stubs.StubFunc(&consul.Init, nil)
	suite.stubs.StubFunc(&backend.Init, nil)
	suite.stubs.StubFunc(&cluster.Init, nil)
	suite.stubs.StubFunc(&route.Init, nil)
	// suite.stubs.StubFunc(&consul.WatchVersionInfoChange, make(<-chan string), nil)

	// mock consul的监听方法
	suite.stubs.StubFunc(&consul.WatchVersionInfoChange, ch, nil)
	ch <- "test"

	// mock掉consul的服务心跳
	suite.stubs.StubFunc(&consul.ServiceRegister, nil)
	suite.stubs.StubFunc(&consul.ServiceDeregister, nil)
	suite.stubs.StubFunc(&consul.CheckRegister, nil)
	suite.stubs.StubFunc(&consul.CheckPassing, nil)
	suite.stubs.StubFunc(&consul.CheckFail, nil)

	// 关闭所有刷新
	suite.stubs.StubFunc(&cluster.Refresh, nil)
	suite.stubs.StubFunc(&backend.Refresh, nil)
	suite.stubs.StubFunc(&route.Refresh, nil)

	// 关闭所有重载
	suite.stubs.StubFunc(&consul.Reload, nil)
	suite.stubs.StubFunc(&cluster.Reload, nil)
	suite.stubs.StubFunc(&backend.Reload, nil)
	suite.stubs.StubFunc(&route.Reload, nil)
	suite.stubs.StubFunc(&tshttp.ReloadCfg, nil)

	suite.stubs.StubFunc(&route.Query, route.NewExecuteResult("success", 200, nil))
	suite.stubs.StubFunc(&route.Write, route.NewExecuteResult("success", 200, nil))
	suite.stubs.StubFunc(&route.CreateDB, route.NewExecuteResult("success", 200, nil))

	mux := http.NewServeMux()
	suite.hs, _ = tshttp.NewHTTPService(mux)
	// 发送这条消息，会触发http的refresh事件，refresh事件结束后，整个服务可用
}

func (suite *HttpServiceSuite) TearDownSuite() {
	suite.stubs.Reset()
	// 判断是否有错误发生
	suite.ctrl.Finish()
}

// 判断cluster的写入功能是否符合预期
func (suite *HttpServiceSuite) TestHttpServiceWrite() {
	// ch <- "test"
	time.Sleep(3 * time.Second)
	// 增加一个http request
	handler := http.HandlerFunc(suite.hs.WriteHandler)
	rr := httptest.NewRecorder()
	request, _ := http.NewRequest("POST", "/write", strings.NewReader("proc,mytag=1 myfield=90"))
	params := request.URL.Query()
	params.Set("db", "db1")
	request.URL.RawQuery = params.Encode()

	// 实际请求
	handler.ServeHTTP(rr, request)
	// 判断返回是否正确
	assert.Equal(suite.T(), 200, rr.Code)
}

// 判断cluster的写入功能是否符合预期
func (suite *HttpServiceSuite) TestHttpServiceQuery() {
	// ch <- "test"
	time.Sleep(3 * time.Second)
	// 增加一个http request
	handler := http.HandlerFunc(suite.hs.QueryHandler)
	rr := httptest.NewRecorder()
	request, _ := http.NewRequest("GET", "/query", strings.NewReader(""))
	params := request.URL.Query()
	params.Set("db", "db")
	params.Set("q", `select * from bb.sshh."tab.\"le1";`)
	request.URL.RawQuery = params.Encode()

	// 实际请求
	handler.ServeHTTP(rr, request)
	// 判断返回是否正确
	assert.Equal(suite.T(), 200, rr.Code)
}

// 判断cluster的写入功能是否符合预期
func (suite *HttpServiceSuite) TestHttpServiceCreateDatabase() {
	// ch <- "test"
	time.Sleep(3 * time.Second)
	// 增加一个http request
	handler := http.HandlerFunc(suite.hs.CreateDBHandler)
	rr := httptest.NewRecorder()
	request, _ := http.NewRequest("POST", "/query", strings.NewReader("create database"))
	params := request.URL.Query()
	params.Set("db", "sss")
	params.Set("q", "create database dds")
	request.URL.RawQuery = params.Encode()

	// 实际请求
	handler.ServeHTTP(rr, request)
	// 判断返回是否正确
	assert.Equal(suite.T(), 200, rr.Code)
}

// 判断cluster的写入功能是否符合预期
func (suite *HttpServiceSuite) TestHttpServiceReload() {
	time.Sleep(3 * time.Second)
	// 增加一个http request
	handler := http.HandlerFunc(suite.hs.ReloadHandler)
	rr := httptest.NewRecorder()
	request, _ := http.NewRequest("GET", "/reload", strings.NewReader(""))
	// 实际请求
	go func() {
		time.Sleep(6 * time.Second)
		ch <- "test"
	}()
	handler.ServeHTTP(rr, request)

	// 判断返回是否正确
	assert.Equal(suite.T(), 204, rr.Code)
}

type formatResult struct {
	src string
	res string
}

type boolResult struct {
	src string
	res bool
}

func (suite *HttpServiceSuite) TestCheckSingleWord() {
	nameList := []boolResult{
		{``, false},
		{`datab1`, true},
		{`nnggg`, true},
		{`abv dd`, false},
		{`fhgn_ttt`, true},
	}

	for i, v := range nameList {
		result := tshttp.CheckSingleWord(v.src)
		suite.Equal(v.res, result, i)
	}
}

// entrance
func TestExampleTestSuite(t *testing.T) {
	suite.Run(t, new(HttpServiceSuite))
}

func BenchmarkInfluxqlRoute(b *testing.B) {
	sql1 := "select * from abc"
	sql2 := "select * from abc where abc=3 limit 20"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		influxql.MatchStatementType(sql1)
		influxql.MatchStatementType(sql2)
	}
}

func BenchmarkInfluxql(b *testing.B) {
	sql1 := "select * from abc"
	sql2 := "select * from abc where abc=3 limit 20"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		influxql.GetDataSourceByStatement(sql1)
		influxql.GetDataSourceByStatement(sql2)
	}
}

func TestLoadInfluxDBRouter(t *testing.T) {
	ctx := context.Background()

	var sentinelAddress []string
	host := "127.0.0.1"
	port := 6379
	password := ""
	masterName := ""
	sentinelPassword := ""
	db := 0

	dialTimeout := time.Minute
	readTimeout := time.Minute

	redisInstance, err := redis.NewRedisClient(
		ctx, &redis.Option{
			Mode:             redis.StandAlone,
			Host:             host,
			Port:             port,
			Password:         password,
			MasterName:       masterName,
			SentinelAddress:  sentinelAddress,
			SentinelPassword: sentinelPassword,
			Db:               db,
			DialTimeout:      dialTimeout,
			ReadTimeout:      readTimeout,
		},
	)

	assert.Nil(t, err)

	prefix := "bkmonitorv3:influxdb"
	router := influxdb.NewRouter(prefix, redisInstance)

	clusterInfo, err := router.GetClusterInfo(ctx)
	assert.Nil(t, err)
	fmt.Println(influxdb.ClusterInfoKey, clusterInfo)

	hostInfo, err := router.GetHostInfo(ctx)
	assert.Nil(t, err)
	fmt.Println(influxdb.HostInfoKey, hostInfo)

	tagInfo, err := router.GetTagInfo(ctx)
	assert.Nil(t, err)
	fmt.Println(influxdb.TagInfoKey, tagInfo)

	hostStatusInfo, err := router.GetHostStatusInfo(ctx)
	assert.Nil(t, err)
	fmt.Println(influxdb.HostStatusInfoKey, hostStatusInfo)

	hostName := "INFLUXDB_NEW_IP1"
	err = router.SetHostStatusRead(ctx, hostName, true)
	assert.Nil(t, err)
}
