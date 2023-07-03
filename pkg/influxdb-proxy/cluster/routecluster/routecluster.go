// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package routecluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"runtime"
	"strings"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

var moduleName = "routecluster"

// Cluster 精确到表级的集群
type Cluster struct {
	name              string
	allBackendList    []backend.Backend
	unreadableHostMap map[string]bool
	lock              sync.RWMutex
	// metric handler
	metric Metrics
	// 计数，用于负载均衡
	balanceMap *BalanceMap
	tagManager *TagInfoManager
}

// NewRouteCluster :
func NewRouteCluster(ctx context.Context, name string, allBackendList []backend.Backend, unreadableHostMap map[string]bool) (cluster.Cluster, error) {
	newCluster := new(Cluster)
	newCluster.name = name
	newCluster.allBackendList = allBackendList
	newCluster.unreadableHostMap = unreadableHostMap
	newCluster.balanceMap = NewBalanceMap(5000)
	newCluster.metric = NewClusterMetric(name)
	newCluster.tagManager = NewTagInfoManager(ctx, name, 2, allBackendList)
	err := newCluster.tagManager.Refresh()
	if err != nil {
		return nil, err
	}
	err = newCluster.tagManager.WatchChange()
	if err != nil {
		return nil, err
	}
	return newCluster, nil
}

func (c *Cluster) checkNilBackend() bool {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"cluster": c.GetName(),
	})
	flowLog.Tracef("called")
	c.lock.RLock()
	flowLog.Tracef("get RLock")
	defer func() {
		c.lock.RUnlock()
		flowLog.Tracef("release RLock")
	}()
	// 遍历检查是否有空backend，这一步必须在写入前处理好，否则会出现少写或多写的情况
	for _, b := range c.allBackendList {
		if b == nil {
			return true
		}
	}

	flowLog.Tracef("done")
	// 增加可写判断，如果没有可写的则视为空
	return false
}

func (c *Cluster) String() string {
	return fmt.Sprintf("influxdb_cluster:[%s],backend_list:%v,unreadable_host:%v,tags:%s", c.GetName(), c.allBackendList, c.unreadableHostMap, c.tagManager)
}

func (c *Cluster) sendIntoBackends(flow uint64, tagKey string, backends []backend.Backend, db, consistency, precision, rp string, header http.Header, reader backend.CopyReader, flowLog *logging.Entry) (*cluster.Response, error) {
	// 遍历并发将数据写入到各个backend中
	var wg sync.WaitGroup
	var (
		stackedError    error
		stackedResponse *backend.Response
	)
	var outResp *backend.Response
	for _, b := range backends {
		flowLog.Tracef("send write request to backend:%s", b)
		wg.Add(1)
		go func(backend2 backend.Backend) {
			defer func() {
				wg.Done()
				// 处理panic信息
				if p := recover(); p != nil {
					err := common.PanicCountInc()
					if err != nil {
						flowLog.Errorf("panic count inc failed,error:%s", err)
					}
					reader.SeekZero()
					buffer, err := io.ReadAll(reader)
					if err != nil {
						flowLog.Errorf("try to read panic data failed,error:%s", err)
					}
					flowLog.Errorf("get panic while writing backend,panic info:%v,panic data:%s", p, buffer)
					// 打印堆栈
					var buf [4096]byte
					n := runtime.Stack(buf[:], false)
					flowLog.Errorf("panic stack ==> %s\n", buf[:n])
				}
			}()
			backendName := backend2.Name()
			// Reader复制
			copyReader := reader.Copy()
			// 此处并不关注是否可写，因为如果不可写，则直接返回异常即可
			c.metric.WriteBackendSendCountInc(backendName, db, tagKey, flowLog)
			writeParams := backend.NewWriteParams(db, consistency, precision, rp)
			resp, err := backend2.Write(flow, writeParams, copyReader, header)
			if err != nil {
				// 错误发生时,记录下来
				flowLog.Errorf("backend->[%s] writes done with err->[%s].", backendName, err)
				c.metric.WriteBackendFailedCountInc(backendName, db, tagKey, flowLog)
				stackedError = err
				return
			}
			if resp.Code >= 300 {
				stackedResponse = resp
			}
			c.metric.WriteBackendSuccessCountInc(backendName, db, tagKey, flowLog)
			if resp != nil {
				outResp = resp
			}
			flowLog.Tracef("backend->[%s] writes done with response:%s", backendName, resp)
		}(b)
	}
	flowLog.Tracef("start to wait backends")
	wg.Wait()
	flowLog.Tracef("wait done")

	// 如果loop写入返回了错误，说明某些backend内部连备份都失败了，这时要进行错误日志及metric处理
	if stackedError != nil {
		flowLog.Errorf("writes done with error:%s", stackedError)
		c.metric.WriteFailedCountInc(db, flowLog)
		return nil, cluster.ErrWriteFailed
	}
	clusterResp := &cluster.Response{}
	if outResp != nil {
		clusterResp.Code = outResp.Code
		clusterResp.Result = outResp.Result
	}
	// 若没有error，检查是否有透传出来的错误code，有则优先返回,但此种情况视为cluster操作成功
	if stackedResponse != nil {
		clusterResp.Code = stackedResponse.Code
		clusterResp.Result = stackedResponse.Result
		flowLog.Warnf("writes done with wrong code,response:%s", stackedResponse)
	}
	c.metric.WriteSuccessCountInc(db, flowLog)
	return clusterResp, nil
}

// Write 逐个backend写入，如果有backend为空,则返回错误,不能启动写入
func (c *Cluster) Write(flow uint64, urlParams *cluster.WriteParams, header http.Header) (*cluster.Response, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
		"cluster": c.GetName(),
	})
	flowLog.Tracef("called,urlParams:%s", urlParams)
	db := urlParams.DB
	c.lock.RLock()
	flowLog.Tracef("get RLock")
	defer func() {
		c.lock.RUnlock()
		flowLog.Tracef("release RLock")
	}()
	c.metric.WriteReceivedCountInc(db, flowLog)
	flowLog.Tracef("start checkNilBackend")
	// 如果集群有空则直接报错
	if c.checkNilBackend() {
		flowLog.Errorf("has nil backend return error")
		c.metric.WriteFailedCountInc(db, flowLog)
		return nil, cluster.ErrMissingBackend
	}
	points := urlParams.Points
	// 如果判断不处理tag路由，则直接拼接所有数据成为reader，然后对cluster的所有backend发送
	if len(urlParams.TagNames) == 0 {
		reader := backend.NewPointsReader(urlParams.AllData, len(points))
		for _, point := range points {
			reader.AppendIndex(point.Start, point.End)
		}
		resp, err := c.sendIntoBackends(flow, "", c.allBackendList, urlParams.DB, urlParams.Consistency, urlParams.Precision, urlParams.RP, header, reader, flowLog)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}

	flowLog.Debugf("get tag name:%#v", urlParams.TagNames)

	// 否则根据tag路由分组，再逐个组写入数据
	tagReaders := make(map[string]backend.CopyReader)
	for _, point := range points {
		tagKey := common.GetTagsKey(urlParams.DB, point.Measurement, urlParams.TagNames, point.Tags)
		flowLog.Debugf("get tag key:%s", tagKey)
		if tagReader, ok := tagReaders[tagKey]; ok {
			tagReader.AppendIndex(point.Start, point.End)
		} else {
			tagReader = backend.NewPointsReader(urlParams.AllData, len(points))
			tagReader.AppendIndex(point.Start, point.End)
			tagReaders[tagKey] = tagReader
		}
	}

	// 其中一个返回的记录
	// 由于有多个backend可能会一起返回，所以此处只需要保留一个正常的即可
	var clusterResp *cluster.Response
	var clusterErr error
	var wg sync.WaitGroup

	for tagKey, reader := range tagReaders {
		backends, err := c.tagManager.GetWriteBackends(tagKey)
		if err != nil {
			flowLog.Errorf("get backend by tag key:%s failed,error:%s", tagKey, err)
			clusterErr = err
			continue
		}
		flowLog.Debugf("get backends:%#v", backends)
		wg.Add(1)
		go func(r backend.CopyReader, key string) {
			defer wg.Done()
			resp, err := c.sendIntoBackends(flow, key, backends, urlParams.DB, urlParams.Consistency, urlParams.Precision, urlParams.RP, header, r, flowLog)
			if err != nil {
				clusterErr = err
				return
			}
			// 只要是正常的返回，给任意一个返回回去都是可以接受的
			clusterResp = resp
		}(reader, tagKey)
	}
	wg.Wait()
	if clusterErr != nil {
		return nil, clusterErr
	}
	return clusterResp, nil
}

// Reset :
func (c *Cluster) Reset(_ string, hostList []string, unreadableHostList []string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"cluster": c.GetName(),
	})
	flowLog.Debugf("start to reset")
	c.lock.Lock()
	flowLog.Tracef("get Lock")
	defer func() {
		c.lock.Unlock()
		flowLog.Tracef("release Lock")
	}()

	flowLog.Tracef("start to GetBackendList")
	allBackendList, emptyList, err := backend.GetBackendList(hostList)
	if err != nil {
		flowLog.Tracef("GetBackendList failed,missing host:%v,error:%s", emptyList, err)
		return err
	}

	// 将list转换为map，以提升效率
	unreadableHostMap := cluster.ConvertListToMap(unreadableHostList)

	c.allBackendList = allBackendList
	c.unreadableHostMap = unreadableHostMap
	c.tagManager.Reset(2, allBackendList)
	flowLog.Debugf("reset done")
	return nil
}

// CreateDatabase :
func (c *Cluster) CreateDatabase(flow uint64, urlParams *cluster.QueryParams, header http.Header) (*cluster.Response, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
		"cluster": c.GetName(),
	})
	flowLog.Tracef("called,urlParams:%s", urlParams)
	db := urlParams.DB
	sql := urlParams.SQL
	// 需要往所有的backend创建数据库
	c.lock.RLock()
	flowLog.Tracef("get RLock")
	defer func() {
		c.lock.RUnlock()
		flowLog.Tracef("release RLock")
	}()
	c.metric.CreateDBReceivedCountInc(db, flowLog)
	flowLog.Tracef("start checkNilBackend")
	// 如果集群有空则直接报错
	if c.checkNilBackend() {
		flowLog.Errorf("has nil backend,return error")
		c.metric.CreateDBFailedCountInc(db, flowLog)
		return nil, cluster.ErrMissingBackend
	}

	// 遍历并发将数据写入到各个backend中
	var wg sync.WaitGroup
	// 如果出现错误，则记录错误的信息,最后通过一次判断决定是否输出错误信息
	var (
		stackedError    error
		stackedResponse *backend.Response
	)
	var outResp *backend.Response
	flowLog.Debugf("start to send createdb request")
	for _, preBackend := range c.allBackendList {
		wg.Add(1)
		go func(b backend.Backend) {
			defer wg.Done()
			flowLog.Tracef("send createdb request to backend:%s", b)
			backendName := b.Name()
			c.metric.CreateDBBackendSendCountInc(backendName, db, flowLog)
			queryParams := backend.NewQueryParams(urlParams.DB, urlParams.SQL, urlParams.Epoch, urlParams.Pretty, urlParams.Chunked, urlParams.ChunkSize)
			resp, err := b.CreateDatabase(flow, queryParams, header)
			if err != nil {
				flowLog.Errorf("backend->[%s] create database done with sql->[%s]  error->[%s]",
					b, sql, err)
				c.metric.CreateDBBackendFailedCountInc(backendName, db, flowLog)
				stackedError = err
			}

			if resp.Code >= 300 {
				stackedResponse = resp
			}
			c.metric.CreateDBBackendSuccessCountInc(backendName, db, flowLog)
			flowLog.Tracef("backend->[%s] create database done with sql->[%s]  response:%s", b, sql, resp)
			// 取最后一次的code和result作为反馈
			outResp = resp
		}(preBackend)
	}
	flowLog.Tracef("start to wait backend")
	wg.Wait()
	// 如果有错误，优先输出错误
	if stackedError != nil {
		flowLog.Errorf("create db has error:%s", stackedError)
		c.metric.CreateDBFailedCountInc(db, flowLog)
		return nil, cluster.ErrCreateDBFailed
	}
	clusterResp := &cluster.Response{}
	// 若没有error，检查是否有透传出来的错误code，有则优先返回,但此种情况视为cluster操作成功
	if stackedResponse != nil {
		flowLog.Warnf("writes done with wrong code,response:%s", stackedResponse)
		clusterResp.Code = stackedResponse.Code
		clusterResp.Result = stackedResponse.Result
		c.metric.CreateDBSuccessCountInc(db, flowLog)
		return clusterResp, nil
	}
	clusterResp.Code = outResp.Code
	clusterResp.Result = outResp.Result
	c.metric.CreateDBSuccessCountInc(db, flowLog)
	flowLog.Debugf("done")
	// 判断是否存在异常
	return clusterResp, nil
}

// GetInfluxVersion :
func (c *Cluster) GetInfluxVersion() string {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"cluster": c.GetName(),
	})
	flowLog.Tracef("called")
	c.lock.RLock()
	flowLog.Tracef("get RLock")
	defer func() {
		c.lock.RUnlock()
		flowLog.Tracef("release RLock")
	}()
	// 如果没有任何后端，返回空字符
	bc := len(c.allBackendList)
	if bc == 0 {
		return ""
	}

	// 随机返回一个后台的版本号

	bi := rand.Intn(bc)
	if c.allBackendList[bi] != nil {
		return c.allBackendList[bi].GetVersion()
	}
	flowLog.Tracef("done")
	return ""
}

// RawQuery :
func (c *Cluster) RawQuery(flow uint64, request *http.Request, tagNames []string) (*http.Response, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
		"cluster": c.GetName(),
	})
	c.lock.RLock()
	flowLog.Tracef("get RLock")
	defer func() {
		c.lock.RUnlock()
		flowLog.Tracef("release RLock")
	}()
	backendList := c.allBackendList
	db := request.Header.Get("db")
	measurement := request.Header.Get("measurement")
	var tagKey string
	c.metric.RawQueryReceivedCountInc(db, flowLog)
	if len(tagNames) != 0 {
		dimensions := request.Header.Get("dimensions")
		if dimensions == "" {
			c.metric.RawQueryFailedCountInc(db, flowLog)
			return nil, ErrMissingRequiredDimensions
		}
		tags, err := common.GetDimensionTag(tagNames, dimensions)
		if err != nil {
			c.metric.RawQueryFailedCountInc(db, flowLog)
			return nil, err
		}
		tagKey = common.GetTagsKey(db, measurement, tagNames, tags)
		if tagKey != "" {
			backendList, err = c.tagManager.GetReadBackends(tagKey)
			if err != nil {
				c.metric.RawQueryFailedCountInc(db, flowLog)
				return nil, err
			}
		}
	}

	backendListLength := int64(len(backendList))
	tempIndex := c.balanceMap.GetCount(tagKey)
	currentIndex := tempIndex
	for (currentIndex - tempIndex) < backendListLength {
		i := currentIndex % backendListLength
		b := backendList[i]
		backendName := b.Name()
		// 判断该集群是否屏蔽该机器的可读
		if _, ok := c.unreadableHostMap[backendName]; ok {
			flowLog.Debugf("backend->[%s] is in not readable list,will try next backend", b)
			currentIndex++
			continue
		}
		// 判断机器可读，不可读直接下一个
		if !b.Readable() {
			flowLog.Warnf("backend->[%s] not ready to be read,will try next backend", b)
			currentIndex++
			continue
		}
		c.metric.RawQueryBackendSendCountInc(b.Name(), db, tagKey, flowLog)
		resp, err := b.RawQuery(flow, request)
		if err != nil {
			c.metric.RawQueryBackendFailedCountInc(b.Name(), db, tagKey, flowLog)
			if err == backend.ErrNetwork || err == backend.ErrReadBody {
				flowLog.Errorf("backend->[%s] failed to query by network/read error->[%s], will try next backend.", b, err)
				currentIndex++
				continue
			}
		}
		c.metric.RawQueryBackendSuccessCountInc(b.Name(), db, tagKey, flowLog)
		c.metric.RawQuerySuccessCountInc(db, flowLog)
		return resp, err
	}
	c.metric.RawQueryFailedCountInc(db, flowLog)
	return nil, fmt.Errorf("influxdb cluster: [%s] has %s", c.name, cluster.ErrNoAvailableBackend)
}

// 遍历
func (c *Cluster) queryInBackends(urlParams *cluster.QueryParams, header http.Header, tempIndex int64, tagKey string, backendList []backend.Backend, flow uint64, flowLog *logging.Entry) (*cluster.Response, error) {
	sql := urlParams.SQL
	db := urlParams.DB

	currentIndex := tempIndex
	backendListLength := int64(len(backendList))
	for (currentIndex - tempIndex) < backendListLength {
		i := currentIndex % backendListLength
		b := backendList[i]
		flowLog.Tracef("going to query q->[%s] db->[%s]  by backend->[%s]", sql, db, b)
		flowLog.Tracef("check if backend is nil")
		// 判空，保底,如果有backend为空，则很容易panic
		if b == nil {
			flowLog.Errorf("found nil backend,query stopped")
			return nil, cluster.ErrMissingBackend
		}
		backendName := b.Name()
		flowLog.Tracef("check if backend is Readable")
		// 判断该集群是否屏蔽该机器的可读
		if _, ok := c.unreadableHostMap[backendName]; ok {
			flowLog.Debugf("backend->[%s] is in not readable list,will try next backend", b)
			currentIndex++
			continue
		}
		// 判断机器可读，不可读直接下一个
		if !b.Readable() {
			flowLog.Warnf("backend->[%s] not ready to be read,will try next backend", b)
			currentIndex++
			continue
		}
		flowLog.Tracef("start to query backend:%s", b)
		c.metric.QueryBackendSendCountInc(backendName, db, tagKey, flowLog)
		queryParams := backend.NewQueryParams(urlParams.DB, urlParams.SQL, urlParams.Epoch, urlParams.Pretty, urlParams.Chunked, urlParams.ChunkSize)
		// 查询
		resp, err := b.Query(flow, queryParams, header)
		if err != nil {
			flowLog.Tracef("query backend->[%s] get error:%s", b, err)
			c.metric.QueryBackendFailedCountInc(backendName, db, tagKey, flowLog)
			// 如果backend返回的错误是url错误或是读取body时的错误，则使用下一个backend查询
			if err == backend.ErrNetwork || err == backend.ErrReadBody {
				flowLog.Errorf("backend->[%s] failed to query by network/read error->[%s], will try next backend.", b, err)
				currentIndex++
				continue
			}
			flowLog.Errorf("backend->[%s] failed to query database for->[%s]", b, err)
			return nil, cluster.ErrQueryFailed
		}
		c.metric.QueryBackendSuccessCountInc(backendName, db, tagKey, flowLog)
		if resp.Code >= 300 {
			flowLog.Warnf("query done with wrong status response:%s", resp)
		}

		clusterResp := &cluster.Response{
			Code:   resp.Code,
			Result: resp.Result,
		}
		return clusterResp, nil
	}
	// 代码走到这里说明所有backend都不可读，也需要报错并计数
	flowLog.Errorf("all backend failed to query->[%s] db->[%s] for network err.", sql, db)
	c.metric.QueryFailedCountInc(db, flowLog)
	return nil, fmt.Errorf("influxdb cluster: [%s] has %s", c.name, cluster.ErrNoAvailableBackend)
}

// 聚合response的value结果
func (c *Cluster) handleInfos(respList []*cluster.Response, flowLog *logging.Entry) (*cluster.Response, error) {
	values := make([][]string, 0)
	var (
		tempInfo *Info
		tempResp *cluster.Response
	)

	repeatMap := make(map[string]bool)
	for _, resp := range respList {
		info := new(Info)
		err := json.Unmarshal([]byte(resp.Result), info)
		if err != nil {
			flowLog.Errorf("unmarshal result failed,error:%s", err)
			return nil, err
		}
		// 目前只应该存在单个result和单个series
		if len(info.Results) == 0 {
			continue
		}
		if len(info.Results[0].Series) == 0 {
			continue
		}
		hasValue := false

		// 去重合并数据，因为两个influxdb可能存在相同维度返回
		// values格式:
		// 这里 bk_biz_id,2 为一行，即一个value
		// key       value
		// ---       -----
		// bk_biz_id 2
		// bk_biz_id 3
		// ip        10.0.1.xx
		// ip        10.0.1.xx
		for _, value := range info.Results[0].Series[0].Values {
			str := strings.Join(value, ",")
			if _, ok := repeatMap[str]; ok {
				continue
			}
			values = append(values, value)
			repeatMap[str] = true
			hasValue = true
		}
		// 取有值的reponse作为基础返回模板
		if tempResp == nil && hasValue {
			tempInfo = info
			tempResp = resp
		}
	}
	// 全都没数据，就拿第一个response直接返回
	if tempInfo == nil {
		flowLog.Debug("empty result")
		return respList[0], nil
	}
	// 用聚合的values替换旧的values
	tempInfo.Results[0].Series[0].Values = values
	result, err := json.Marshal(tempInfo)
	if err != nil {
		flowLog.Warnf("marshal result failed,error:%s", err)
		return nil, err
	}

	if tempResp == nil {
		tempResp = new(cluster.Response)
	} else {
		tempResp.Result = string(result)
	}

	return tempResp, nil
}

func (c *Cluster) getFlowLog(flow uint64) *logging.Entry {
	return logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
		"cluster": c.GetName(),
	})
}

// QueryInfo 遍历路由对应的所有tag，获取一些信息
func (c *Cluster) QueryInfo(flow uint64, urlParams *cluster.QueryParams, header http.Header) (*cluster.Response, error) {
	flowLog := c.getFlowLog(flow)
	flowLog.Tracef("called,urlParams:%s", urlParams)
	db := urlParams.DB
	sql := urlParams.SQL
	c.lock.RLock()
	flowLog.Tracef("get RLock")
	defer func() {
		c.lock.RUnlock()
		flowLog.Tracef("release RLock")
	}()
	c.metric.QueryReceivedCountInc(db, flowLog)
	tagNames := urlParams.TagNames
	var tagKey string
	flowLog.Debugf("get tag Names:%#v", tagNames)
	// 如果传入了tag过滤参数，则启用tag路由
	if len(tagNames) != 0 {
		measurement := urlParams.Measurement
		// 基于db/measurement格式作为前缀，获取数据
		tagKeys := c.tagManager.GetReadKeys(db + "/" + measurement)
		respList := make([]*cluster.Response, 0, len(tagKeys))
		// 每个tagKey都要查一下，最后拼接数据
		for _, key := range tagKeys {
			flowLog.Debugf("get tag Key:%s", key)
			backendList, err := c.tagManager.GetReadBackends(key)
			if err != nil {
				flowLog.Errorf("get backend list failed,key:%s,error:%s", key, err)
				c.metric.QueryFailedCountInc(db, flowLog)
				return nil, err
			}
			flowLog.Debugf("get backend list:%#v by tag key:%s", backendList, key)
			tempIndex := c.balanceMap.GetCount(key)
			response, err := c.queryInBackends(urlParams, header, tempIndex, key, backendList, flow, flowLog)
			if err != nil {
				// 存在任意报错则直接退出
				flowLog.Errorf("query info by sql:%s failed,error:%s", sql, err)
				c.metric.QueryFailedCountInc(db, flowLog)
				return nil, err
			}
			respList = append(respList, response)
		}
		c.metric.QuerySuccessCountInc(db, flowLog)
		flowLog.Debugf("done")
		// 将多个结果去重聚合为同一张表的数据返回
		return c.handleInfos(respList, flowLog)
	}
	backendList := c.allBackendList
	flowLog.Debugf("start to select backend to query")
	tempIndex := c.balanceMap.GetCount(tagKey)
	response, err := c.queryInBackends(urlParams, header, tempIndex, "", backendList, flow, flowLog)
	if err != nil {
		// 代码走到这里说明所有backend都不可读，也需要报错并计数
		flowLog.Errorf("all backend failed to query->[%s] db->[%s] for network err.", sql, db)
		c.metric.QueryFailedCountInc(db, flowLog)
		return nil, err
	}
	// 能走到这里说明query执行成功且没有错误，所以增加成功计数
	c.metric.QuerySuccessCountInc(db, flowLog)
	flowLog.Debugf("done")
	// 没有分维度的情况下，不需要解析聚合数据，直接返回即可
	return response, nil
}

// Query ;
func (c *Cluster) Query(flow uint64, urlParams *cluster.QueryParams, header http.Header) (*cluster.Response, error) {
	flowLog := c.getFlowLog(flow)
	flowLog.Tracef("called,urlParams:%s", urlParams)
	db := urlParams.DB
	measurement := urlParams.Measurement
	sql := urlParams.SQL
	c.lock.RLock()
	flowLog.Tracef("get RLock")
	defer func() {
		c.lock.RUnlock()
		flowLog.Tracef("release RLock")
	}()
	c.metric.QueryReceivedCountInc(db, flowLog)
	tagNames := urlParams.TagNames
	var tagKey string
	flowLog.Debugf("get tag Names:%#v", tagNames)
	backendList := c.allBackendList
	// 如果传入了tag过滤参数，则启用tag路由
	if len(tagNames) != 0 {
		tags, err := common.GetSelectTag(tagNames, sql)
		if err != nil {
			flowLog.Errorf("get tag failed,wrong format sql?error:%s", err)
			return nil, cluster.ErrGetTagValueFailed
		}
		tagKey = common.GetTagsKey(db, measurement, tagNames, tags)
		flowLog.Debugf("get tag Key:%s", tagKey)
		backendList, err = c.tagManager.GetReadBackends(tagKey)
		if err != nil {
			return nil, err
		}
		flowLog.Debugf("get backend list:%#v", backendList)
	}
	flowLog.Debugf("start to select backend to query")
	tempIndex := c.balanceMap.GetCount(tagKey)
	response, err := c.queryInBackends(urlParams, header, tempIndex, tagKey, backendList, flow, flowLog)
	if err != nil {
		// 代码走到这里说明所有backend都不可读，也需要报错并计数
		flowLog.Errorf("all backend failed to query->[%s] db->[%s] for network err.", sql, db)
		c.metric.QueryFailedCountInc(db, flowLog)
		return nil, err
	}
	// 能走到这里说明query执行成功且没有错误，所以增加成功计数
	c.metric.QuerySuccessCountInc(db, flowLog)
	flowLog.Debugf("done")
	return response, nil
}

// Wait routecluster本身不负责管理backend，所以等待也不应该是他等
func (c *Cluster) Wait() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"cluster": c.GetName(),
	})
	flowLog.Tracef("called")
	flowLog.Tracef("done")
	return
}

// GetName :
func (c *Cluster) GetName() string {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"cluster": c.name,
	})
	flowLog.Tracef("called")
	flowLog.Tracef("done")
	return c.name
}

func init() {
	cluster.RegisterCluster("routecluster", NewRouteCluster)
}
