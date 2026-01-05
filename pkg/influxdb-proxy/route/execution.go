// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package route

import (
	"bytes"
	"fmt"
	"net/http"
	"sync"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/route/influxql"
)

// execution路由集合
func (m *Manager) getQueryExecution(params *QueryParams, flowLog *logging.Entry) QueryExecution {
	sql := params.SQL
	stmtType, err := influxql.MatchStatementType(sql)
	if err != nil {
		flowLog.Errorf("match sql statament type failed,error:%s", err)
		return m.wrongRequestExecution
	}

	var execution QueryExecution

	// 根据type选择返回哪个execution
	switch stmtType {
	case influxql.StmtSelect:
		execution = m.selectExecution
	case influxql.StmtShowDatabases:
		execution = m.showDatabasesExecution
	case influxql.StmtShowMeasurements:
		execution = m.showMeasurementsExecution
	case influxql.StmtShowSeries:
		execution = m.showSeriesExecution
	case influxql.StmtShowTagKeys:
		execution = m.showTagKeysExecution
	case influxql.StmtShowTagValues:
		execution = m.showTagValuesExecution
	case influxql.StmtShowFieldKeys:
		execution = m.showFieldKeysExecution
	default:
		execution = m.wrongRequestExecution

	}
	return execution
}

func (m *Manager) getWriteExecution(_ *WriteParams, _ *logging.Entry) WriteExecution { //nolint
	return m.writeExecution
}

func (m *Manager) getCreateDBExecution(_ *CreateDBParams, _ *logging.Entry) CreateDBExecution { //nolint
	return m.createDBExecution
}

// writeExecution 写入语句处理
func (m *Manager) writeExecution(params *WriteParams, flowLog *logging.Entry) *ExecuteResult {
	db := params.DB
	consistency := params.Consistency
	precision := params.Precision
	rp := params.RP
	allPoints := params.Data
	header := params.Header
	flow := params.Flow

	// 给个默认值，防止无操作报空
	if len(allPoints) == 0 {
		flowLog.Errorf("get empty data")
		return NewExecuteResult(fmt.Sprintf(errTemplate, "empty line"), outerFail, ErrEmptyData)
	}

	// 如果末尾没有\n,就增加一个
	if !bytes.HasSuffix(allPoints, []byte("\n")) {
		allPoints = append(allPoints, []byte("\n")...)
	}

	// 记录抽样结果
	var stackedError error
	var clusterResp *cluster.Response
	var stackedResp *cluster.Response
	var mu sync.Mutex

	batchSize := viper.GetInt(common.ConfigHTTPBatchsize)
	wg := &sync.WaitGroup{}
	flowLog.Debugf("start write")
	pointsChannel := make(chan common.Points)
	sem := make(chan struct{}, 100)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for totalPoints := range pointsChannel {
			// 将收到的points按库表分组
			pointsMap := make(map[string]common.Points)
			for _, point := range totalPoints {
				route := FormatRoute(point.DB, point.Measurement)
				if routePoints, ok := pointsMap[route]; !ok {
					routePoints = make(common.Points, 0, batchSize)
					routePoints = append(routePoints, point)
					pointsMap[route] = routePoints
				} else {
					routePoints = append(routePoints, point)
					pointsMap[route] = routePoints
				}
			}

			for route, points := range pointsMap {
				dbCluster, err := GetRouteCluster(flow, route)
				if err != nil {
					flowLog.Errorf("failed to get cluster for->[%s]", err)
					mu.Lock()
					stackedError = ErrMatchClusterByRouteFailed
					mu.Unlock()
					continue
				}
				_ = WriteClusterSendCountInc(dbCluster.GetName(), db)
				sem <- struct{}{}
				wg.Add(1)
				go func(route string, points common.Points, dbCluster cluster.Cluster) {
					defer wg.Done()
					defer func() { <-sem }() // 释放信号量
					tagNames := m.tagMap[route]
					params := cluster.NewWriteParams(db, consistency, precision, rp, points, allPoints, tagNames)
					localResp, err := dbCluster.Write(flow, params, header)
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						flowLog.Errorf("write to cluster->[%s],failed,error:%s", dbCluster.GetName(), err)
						stackedError = ErrClusterWriteFailed
						_ = WriteClusterFailedCountInc(dbCluster.GetName(), db)
						return
					}
					if localResp.Code >= 300 {
						flowLog.Warnf("write to cluster->[%s] get wrong status code,response:%s", dbCluster.GetName(), localResp)
						stackedResp = localResp
					}
					_ = WriteClusterSuccessCountInc(dbCluster.GetName(), db)
					// 保留一个成功响应作为默认值 (如果还没有的话)
					if clusterResp == nil && localResp.Code < 300 {
						clusterResp = localResp
					}
				}(route, points, dbCluster)
			}
		}
	}()
	// 数据在这里处理，并通过channel传入到上面的goroutine
	err := AnaylizeTagData(flow, pointsChannel, db, batchSize, allPoints, flowLog)
	if err != nil {
		flowLog.Errorf("anaylize tag data failed,error:%s", err)
		mu.Lock()
		stackedError = err
		mu.Unlock()
	}

	close(pointsChannel)
	flowLog.Debugf("wait for write done")
	defer flowLog.Debugf("write done")
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if stackedError != nil {
		flowLog.Errorf("write failed for->[%s]", stackedError.Error())
		result := NewExecuteResult(fmt.Sprintf(errTemplate, stackedError.Error()), innerFail, stackedError)
		return result
	}
	if stackedResp != nil {
		flowLog.Warnf("get wrong status code after write data,response:%s", stackedResp)
		result := NewExecuteResult(stackedResp.Result, stackedResp.Code, nil)
		return result
	}
	if clusterResp != nil {
		return NewExecuteResult(clusterResp.Result, clusterResp.Code, nil)
	}
	return NewExecuteResult(clusterResp.Result, clusterResp.Code, nil)
}

// createDBExecution 建库语句处理
func (m *Manager) createDBExecution(params *CreateDBParams, flowLog *logging.Entry) *ExecuteResult {
	db := params.DB
	// 获取集群名，这里是自定义的接口，不是infludb的透传
	clusterName := params.Cluster
	flow := params.Flow
	header := params.Header
	// 拼接sql
	flowLog.Tracef("combine sql")
	sql := fmt.Sprintf(`create database "%s"`, db)
	flowLog.Tracef("combined sql:%s", sql)
	// 根据集群名获取集群,传入指针减少消耗
	flowLog.Tracef("start to get cluster,cluster name:%s", clusterName)
	dbCluster, err := GetRouteClusterByName(flow, clusterName)
	if err != nil {
		flowLog.Errorf("get cluster failed,clusterName:%s,error:%s", clusterName, err)
		return NewExecuteResult("get cluster failed", outerFail, err)
	}
	// 建库语句不需要在db位声明db,所以是空字符串
	urlParams := cluster.NewQueryParams("", "", sql, "", "", "", "", nil)
	flowLog.Tracef("urlParams:%s", urlParams)
	// 执行建库语句
	metricError(moduleName, CreateDBClusterSendCountInc(dbCluster.GetName(), db), flowLog)
	resp, err := dbCluster.CreateDatabase(flow, urlParams, header)
	if err != nil {
		metricError(moduleName, CreateDBClusterFailedCountInc(dbCluster.GetName(), db), flowLog)
		flowLog.Errorf("failed to create database for->[%s]", err)
		return NewExecuteResult(fmt.Sprintf(errTemplate, err), innerFail, err)
	}
	if resp.Code >= 300 {
		flowLog.Warnf("get error response after create db to cluster:%s,response:%s", dbCluster.GetName(), resp)
	}

	return NewExecuteResult(resp.Result, resp.Code, nil)
}

// showDatabasesExecution 获取数据库列表，通过consul获取
// 注：因为是通过consul获取的数据，所以与实际的influxdb实例会有偏差，可能会出现influxdb实例中存在，却无法通过该接口查询到的db
func (m *Manager) showDatabasesExecution(params *QueryParams, flowLog *logging.Entry) *ExecuteResult {
	sql := params.SQL
	flowLog.Tracef("start to do show databases,q:%s", sql)
	dbs, err := GetDBNames()
	if err != nil {
		flowLog.Errorf("failed to get db name ,error;%s", err)
		result := NewExecuteResult(fmt.Sprintf(errTemplate, err), outerFail, err)
		return result
	}
	var dbstr string
	for idx, v := range dbs {
		if idx != 0 {
			dbstr = dbstr + ","
		}
		dbstr = dbstr + `["` + v + `"]`
	}

	return NewExecuteResult(`{"results":[{"Series":[{"name":"databases","columns":["name"],"values":[`+dbstr+`]}],"Messages":null}]}`, normalSucc, nil)
}

// showDatabasesExecution 获取表名列表，通过consul获取
// 注：因为是通过consul获取的数据，所以与实际的influxdb实例会有偏差，可能会出现influxdb实例中存在，却无法通过该接口查询到的measurements
func (m *Manager) showMeasurementsExecution(params *QueryParams, flowLog *logging.Entry) *ExecuteResult {
	db := params.DB
	sql := params.SQL
	flowLog.Tracef("start to do show measurements,q:%s", sql)

	// 根据sql获取新参数
	dataSource, err := influxql.GetSingleDataSource(sql)
	if err != nil {
		return NewExecuteResult(fmt.Sprintf(errTemplate, err), outerFail, err)
	}

	// 语句里的db优先度最高
	if dataSource.GetDB() != "" {
		db = dataSource.GetDB()
	}

	if err := CheckDB(db); err != nil {
		flowLog.Errorf("check db failed,error:%s", err)
		result := NewExecuteResult(fmt.Sprintf(errTemplate, err), outerFail, err)
		return result
	}

	// 从consul获取表名列表
	tbs, err := GetTableNames(db)
	if err != nil {
		flowLog.Errorf("failed to get tablename ,error;%s", err)
		result := NewExecuteResult(fmt.Sprintf(errTemplate, err), outerFail, err)
		return result
	}
	var tbstr string
	for idx, v := range tbs {
		if idx != 0 {
			tbstr = tbstr + ","
		}
		tbstr = tbstr + `["` + v + `"]`
	}
	return NewExecuteResult(`{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name"],"values":[[`+tbstr+`]]}]}]}`, normalSucc, nil)
}

func (m *Manager) rawQueryExecution(flow uint64, request *http.Request, flowLog *logging.Entry) (*http.Response, error) {
	db := request.Header.Get("db")
	table := request.Header.Get("measurement")
	route := FormatRoute(db, table)
	// 根据数据库和表名拿到集群
	dbCluster, err := GetRouteCluster(flow, route)
	if err != nil {
		flowLog.Errorf("failed to get cluster to handler request for->[%s],db:%s", err, db)
		return nil, err
	}
	tagNames := m.tagMap[route]
	return dbCluster.RawQuery(flow, request, tagNames)
}

func (m *Manager) handleResult(dbCluster cluster.Cluster, flow uint64, urlParams *cluster.QueryParams, header http.Header, flowLog *logging.Entry, db, action string) *ExecuteResult {
	var err error
	var resp *cluster.Response
	if action == "query" {
		resp, err = dbCluster.Query(flow, urlParams, header)
	} else {
		resp, err = dbCluster.QueryInfo(flow, urlParams, header)
	}
	if err != nil {
		metricError(moduleName, QueryClusterFailedCountInc(dbCluster.GetName(), db), flowLog)
		flowLog.Errorf("failed to query for->[%s],params:%s", err, urlParams)
		result := NewExecuteResult(fmt.Sprintf(errTemplate, err), innerFail, err)
		return result
	}
	if resp.Code >= 300 {
		flowLog.Warnf("get error response after query data from cluster:%s,response:%s", dbCluster.GetName(), resp)
	}
	return NewExecuteResult(resp.Result, resp.Code, nil)
}

func (m *Manager) basicQueryAction(db, sql, epoch, pretty, chunked, chunkSize string, header http.Header, flow uint64, flowLog *logging.Entry) *ExecuteResult {
	// 根据sql获取新参数
	dataSource, err := influxql.GetSingleDataSource(sql)
	if err != nil {
		return NewExecuteResult(fmt.Sprintf(errTemplate, err), outerFail, err)
	}
	// 语句里的db优先度最高
	if dataSource.GetDB() != "" {
		db = dataSource.GetDB()
	}
	table := dataSource.GetTable()

	flowLog.Tracef("start to get cluster by route,db:%stable:%s,sql:%s", db, table, sql)

	route := FormatRoute(db, table)
	// 根据数据库和表名拿到集群
	dbCluster, err := GetRouteCluster(flow, route)
	if err != nil {
		flowLog.Errorf("failed to get cluster to handler request for->[%s],db:%s,sql:%s", err, db, sql)
		result := NewExecuteResult(fmt.Sprintf(errTemplate, err), outerFail, err)
		return result
	}
	tagNames := m.tagMap[route]
	urlParams := cluster.NewQueryParams(db, table, sql, epoch, pretty, chunked, chunkSize, tagNames)
	flowLog.Tracef("urlParams:%s", urlParams)
	// 执行
	flowLog.Debugf("start query")
	metricError(moduleName, QueryClusterSendCountInc(dbCluster.GetName(), db), flowLog)
	return m.handleResult(dbCluster, flow, urlParams, header, flowLog, db, "query")
}

func (m *Manager) basicQueryInfoAction(db, sql, epoch, pretty, chunked, chunkSize string, header http.Header, flow uint64, flowLog *logging.Entry) *ExecuteResult {
	// 根据sql获取新参数
	dataSource, err := influxql.GetSingleDataSource(sql)
	if err != nil {
		flowLog.Errorf("failed to get data source from sql->[%s] for->[%s]", sql, err)
		return NewExecuteResult(fmt.Sprintf(errTemplate, err), outerFail, err)
	}
	// 语句里的db优先度最高
	if dataSource.GetDB() != "" {
		db = dataSource.GetDB()
	}
	table := dataSource.GetTable()

	flowLog.Tracef("start to get cluster by route,db:%stable:%s,sql:%s", db, table, sql)

	route := FormatRoute(db, table)
	// 根据数据库和表名拿到集群
	dbCluster, err := GetRouteCluster(flow, route)
	if err != nil {
		flowLog.Errorf("failed to get cluster to handler request for->[%s] db->[%s] sql->[%s] table->[%s]", err, db, sql, table)
		result := NewExecuteResult(fmt.Sprintf(errTemplate, err), outerFail, err)
		return result
	}
	tagNames := m.tagMap[route]
	urlParams := cluster.NewQueryParams(db, table, sql, epoch, pretty, chunked, chunkSize, tagNames)
	flowLog.Tracef("urlParams:%s", urlParams)
	// 执行
	flowLog.Debugf("start query")
	metricError(moduleName, QueryClusterSendCountInc(dbCluster.GetName(), db), flowLog)
	return m.handleResult(dbCluster, flow, urlParams, header, flowLog, db, "queryInfo")
}

// 匹配失败的默认execution
func (m *Manager) wrongRequestExecution(params *QueryParams, flowLog *logging.Entry) *ExecuteResult {
	flowLog.Errorf("match execution by sql failed,db:%s,sql:%s", params.DB, params.SQL)
	return NewExecuteResult(fmt.Sprintf(errTemplate, ErrSQLNotSupported), outerFail, ErrSQLNotSupported)
}

// selectExecution 标准select语句处理
func (m *Manager) selectExecution(params *QueryParams, flowLog *logging.Entry) *ExecuteResult {
	return m.basicQueryAction(params.DB, params.SQL, params.Epoch, params.Pretty,
		params.Chunked, params.ChunkSize, params.Header, params.Flow, flowLog)
}

// showSeriesExecution show series语句处理
func (m *Manager) showSeriesExecution(params *QueryParams, flowLog *logging.Entry) *ExecuteResult {
	return m.basicQueryInfoAction(params.DB, params.SQL, params.Epoch, params.Pretty,
		params.Chunked, params.ChunkSize, params.Header, params.Flow, flowLog)
}

// showTagKeysExecution show tag keys语句处理
func (m *Manager) showTagKeysExecution(params *QueryParams, flowLog *logging.Entry) *ExecuteResult {
	return m.basicQueryInfoAction(params.DB, params.SQL, params.Epoch, params.Pretty,
		params.Chunked, params.ChunkSize, params.Header, params.Flow, flowLog)
}

// showTagValuesExecution show tag values语句处理
func (m *Manager) showTagValuesExecution(params *QueryParams, flowLog *logging.Entry) *ExecuteResult {
	return m.basicQueryInfoAction(params.DB, params.SQL, params.Epoch, params.Pretty,
		params.Chunked, params.ChunkSize, params.Header, params.Flow, flowLog)
}

// showFieldKeysExecution show field keys语句处理
func (m *Manager) showFieldKeysExecution(params *QueryParams, flowLog *logging.Entry) *ExecuteResult {
	return m.basicQueryInfoAction(params.DB, params.SQL, params.Epoch, params.Pretty,
		params.Chunked, params.ChunkSize, params.Header, params.Flow, flowLog)
}
