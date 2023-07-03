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
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/route"
)

// SwitchHandler :控制服务开关
func (httpService *Service) SwitchHandler(writer http.ResponseWriter, request *http.Request) { //nolint
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Infof("start to switch proxy")

	var err error
	err = httpService.switchAvailable(httpService.address, !httpService.available)
	if err != nil {
		flowLog.Errorf("switchAvailable failed,error:%s", err)
		writer.WriteHeader(innerFail)
		_, err := writer.Write([]byte(fmt.Sprintf(errTemplate, fmt.Sprintf("switch available failed,error:%s", err))))
		if err != nil {
			flowLog.Errorf("writer write failed,error:%s", err)
			return
		}
		return
	}

	flowLog.Tracef("service available state switched into:%t", httpService.available)
	flowLog.Infof("switch successful, current state:%t", httpService.available)
}

// PrintHandler :
func (httpService *Service) PrintHandler(writer http.ResponseWriter, request *http.Request) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": common.GetFlow(request),
	})
	flowLog.Infof("start to print proxy info")
	str := backend.Print() + cluster.Print() + route.Print()
	flowLog.Tracef("print response:%s", str)
	_, err := writer.Write([]byte(str))
	if err != nil {
		flowLog.Errorf("writer write failed,error:%s", err)
		return
	}
	flowLog.Infof("print done")
	return
}

// DebugHandler 开启debug模式,传入operation=close时可以关闭
func (httpService *Service) DebugHandler(writer http.ResponseWriter, request *http.Request) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": common.GetFlow(request),
	})
	flowLog.Infof("start to switch debug")
	operation := request.URL.Query().Get("operation")
	if operation == "close" {
		runtime.SetBlockProfileRate(0)
		runtime.SetMutexProfileFraction(0)
		flowLog.Tracef("block and mutex profile stop watching")
		_, err := writer.Write([]byte("block and mutex profile stop watching\n"))
		if err != nil {
			flowLog.Errorf("writer write failed,error:%s", err)
			return
		}
		return
	}
	if operation == "open" {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)
		flowLog.Tracef("block and mutex profile start watching")
		_, err := writer.Write([]byte("block and mutex profile start watching\n"))
		if err != nil {
			flowLog.Errorf("writer write failed,error:%s", err)
			return
		}
		return
	}
	writer.WriteHeader(outerFail)
	flowLog.Tracef("get debug request but no opertaion found")
	_, err := writer.Write([]byte("no operation found\n"))
	if err != nil {
		flowLog.Errorf("writer write failed,error:%s", err)
		return
	}
	flowLog.Infof("switch debug successful")
	return
}

// ReloadHandler 重载配置, undone
func (httpService *Service) ReloadHandler(writer http.ResponseWriter, request *http.Request) {
	flowID := common.GetFlow(request)
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flowID,
	})
	flowLog.Infof("get reload request")
	metricError(moduleName, ReloadReceivedCountInc(), flowLog)
	defer func() { _ = request.Body.Close() }()
	var err error
	// 启动reload流程
	err = httpService.Reload(flowID)
	if err != nil {
		flowLog.Errorf("reload failed,error:%s", err)
		writer.WriteHeader(innerFail)
		// ReloadFailInc()
		metricError(moduleName, ReloadFailedCountInc(strconv.Itoa(innerFail)), flowLog)
		_, err := writer.Write([]byte(fmt.Sprintf(errTemplate, err)))
		if err != nil {
			flowLog.Errorf("writer write failed,error:%s", err)
			return
		}
		return
	}
	// 重载配置完成，返回空body
	flowLog.Debugf("send response")
	writer.WriteHeader(noContent)
	// ReloadSuccessInc()
	metricError(moduleName, ReloadSuccessCountInc(strconv.Itoa(noContent)), flowLog)
	metricError(moduleName, ProxyReloadRecord(time.Now().Unix()), flowLog)
	flowLog.Infof("reload done")
	return
}

// RawQueryHandler :
func (httpService *Service) RawQueryHandler(writer http.ResponseWriter, request *http.Request) {
	flow := common.GetFlow(request)
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
	})
	db := request.Header.Get("db")
	resp, err := route.RawQuery(flow, request, flowLog)
	if err != nil {
		flowLog.Errorf("raw query failed,error:%s", err)
		RawQueryFailedCountInc(db, "500")
		httpService.writeBackJson(writer, err.Error(), 500, flowLog)
		return
	}

	resHeader := writer.Header()
	for key, valueList := range resp.Header {
		for _, value := range valueList {
			resHeader.Add(key, value)
		}
	}
	result, err := io.ReadAll(resp.Body)
	if err != nil {
		flowLog.Errorf("raw query failed,error:%s", err)
		RawQueryFailedCountInc(db, "500")
		httpService.writeBackJson(writer, err.Error(), 500, flowLog)
		return
	}
	// 透传错误码，但做好记录
	if resp.StatusCode >= 400 {
		flowLog.Errorf("get wrong status code when execute raw query:%d,message:%s", resp.StatusCode, result)
	}
	RawQuerySuccessCountInc(db, strconv.Itoa(resp.StatusCode))
	httpService.writeBack(writer, string(result), resp.StatusCode, flowLog)

	return
}

// QueryHandler :
func (httpService *Service) QueryHandler(writer http.ResponseWriter, request *http.Request) {
	flow := common.GetFlow(request)
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
	})
	flowLog.Debugf("query start")

	// 从request里获得参数
	db := strings.TrimSpace(request.FormValue("db"))
	sql := strings.TrimSpace(request.FormValue("q"))
	epoch := strings.TrimSpace(request.FormValue("epoch"))
	pretty := strings.TrimSpace(request.FormValue("pretty"))
	chunked := strings.TrimSpace(request.FormValue("chunked"))
	chunkSize := strings.TrimSpace(request.FormValue("chunk_size"))
	params := strings.TrimSpace(request.FormValue("params"))
	header := request.Header

	// 拼装成params
	queryParams := route.NewQueryParams(db, sql, epoch, pretty, chunked, chunkSize, params, header, flow)

	// 执行
	result := route.Query(queryParams, flowLog)
	if result.Err != nil {
		// 这里记录失败metric
		metricError(moduleName, QueryFailedCountInc(db, strconv.Itoa(result.Code)), flowLog)
		httpService.writeBack(writer, result.Message, result.Code, flowLog)
		return
	}

	if header.Get("accept") != "" {
		writer.Header().Add("Content-type", header.Get("accept"))
	} else {
		writer.Header().Add("Content-type", "application/json")
	}

	// 这里记录成功metric
	metricError(moduleName, QuerySuccessCountInc(db, strconv.Itoa(result.Code)), flowLog)

	// 返回信息
	httpService.writeBack(writer, result.Message, result.Code, flowLog)
}

// WriteHandler  :
func (httpService *Service) WriteHandler(writer http.ResponseWriter, request *http.Request) {
	flow := common.GetFlow(request)
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
	})
	flowLog.Debugf("write start")
	// 获取参数
	db := strings.TrimSpace(request.URL.Query().Get("db"))
	precision := strings.TrimSpace(request.URL.Query().Get("precision"))
	consistency := strings.TrimSpace(request.URL.Query().Get("consistency"))
	rp := strings.TrimSpace(request.URL.Query().Get("rp"))

	header := request.Header
	flowLog.Tracef("get db name from url,db:%s", db)

	data, err := readRequestBody(request, flowLog)
	if err != nil {
		metricError(moduleName, WriteFailedCountInc(db, strconv.Itoa(outerFail)), flowLog)
		httpService.writeBack(writer, fmt.Sprintf(errTemplate, err), outerFail, flowLog)
		return
	}

	// 拼接param
	writeParams := route.NewWriteParams(db, precision, consistency, rp, data, header, flow)

	// 执行
	result := route.Write(writeParams, flowLog)

	// 执行
	if result.Err != nil {
		// 这里记录失败metric
		metricError(moduleName, WriteFailedCountInc(db, strconv.Itoa(result.Code)), flowLog)
		httpService.writeBack(writer, result.Message, result.Code, flowLog)
		return
	}

	// 这里记录成功metric
	metricError(moduleName, WriteSuccessCountInc(db, strconv.Itoa(result.Code)), flowLog)
	// 返回结果
	httpService.writeBack(writer, result.Message, result.Code, flowLog)
}

// CreateDBHandler :
func (httpService *Service) CreateDBHandler(writer http.ResponseWriter, request *http.Request) {
	flow := common.GetFlow(request)
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
	})
	flowLog.Debugf("create db start")

	// 从request里获得参数
	db := strings.TrimSpace(request.FormValue("db"))
	cluster := strings.TrimSpace(request.FormValue("cluster"))
	header := request.Header

	// 拼装成params
	createDBParams := route.NewCreateDBParams(db, cluster, header, flow)

	// 执行
	result := route.CreateDB(createDBParams, flowLog)
	if result.Err != nil {
		// 这里记录失败metric
		metricError(moduleName, CreateDBFailedCountInc(db, strconv.Itoa(result.Code)), flowLog)
		httpService.writeBack(writer, result.Message, result.Code, flowLog)
		return
	}

	// 这里记录成功metric
	metricError(moduleName, CreateDBSuccessCountInc(db, strconv.Itoa(result.Code)), flowLog)

	// 返回信息
	httpService.writeBack(writer, result.Message, result.Code, flowLog)
}
