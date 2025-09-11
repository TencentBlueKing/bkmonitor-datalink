// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/influxdata/influxdb/prometheus/remote"
	oleltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

var (
	once           sync.Once
	influxDBRouter *Router

	hostMapInc  = make(map[string]int)
	hostMapLock = new(sync.Mutex)

	DivideSymbol  = "###"
	EqualSymbol   = "=="
	DefaultTagKey = "__default__/__default__/__default__==__default__"
)

type Router struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
	wg         *sync.WaitGroup
	lock       *sync.RWMutex

	router influxdb.Router

	clusterInfo     influxdb.ClusterInfo
	hostInfo        influxdb.HostInfo
	tagInfo         influxdb.TagInfo
	proxyInfo       influxdb.ProxyInfo
	queryRouterInfo influxdb.QueryRouterInfo
	endpointSet     *endpointSet

	hostStatusInfo influxdb.HostStatusInfo
}

func MockRouter(proxyInfo influxdb.ProxyInfo, queryRouterInfo influxdb.QueryRouterInfo) {
	ir := GetInfluxDBRouter()
	ir.lock.Lock()
	defer ir.lock.Unlock()

	if ir.proxyInfo == nil {
		ir.proxyInfo = make(influxdb.ProxyInfo)
	}
	for k, v := range proxyInfo {
		ir.proxyInfo[k] = v
	}
	if ir.queryRouterInfo == nil {
		ir.queryRouterInfo = make(influxdb.QueryRouterInfo)
	}
	for k, v := range queryRouterInfo {
		ir.queryRouterInfo[k] = v
	}
}

func GetInfluxDBRouter() *Router {
	once.Do(func() {
		influxDBRouter = &Router{
			wg:   new(sync.WaitGroup),
			lock: new(sync.RWMutex),
		}
	})
	return influxDBRouter
}

func (r *Router) ReloadRouter(ctx context.Context, prefix string, dialOpts []grpc.DialOption) error {
	var err error
	err = r.Stop()
	if err != nil {
		return err
	}

	r.ctx, r.cancelFunc = context.WithCancel(ctx)
	r.router = influxdb.NewRouter(prefix, redis.Client())
	r.endpointSet = NewEndpointSet(func() []*BackendRef {
		bs := make([]*BackendRef, len(r.hostInfo))
		i := 0
		for _, h := range r.hostInfo {
			var address string
			if h.Protocol == GRPC {
				address = fmt.Sprintf("%s:%d", h.DomainName, h.GrpcPort)
			} else {
				address = fmt.Sprintf("%s:%d", h.DomainName, h.Port)
			}
			bs[i] = &BackendRef{
				Address:  address,
				Protocol: h.Protocol,
			}
			i++
		}
		return bs
	}, dialOpts)

	err = r.ReloadAllKey(r.ctx)
	return err
}

func (r *Router) Ping(ctx context.Context, timeout time.Duration, pingCount int) {
	// 不存在 host 信息则直接返回
	if r.hostInfo == nil || len(r.hostInfo) == 0 {
		return
	}

	// 开始进行 Ping influxdb
	clint := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	for _, v := range r.hostInfo {
		// 重试 pingCount 次数
		var read bool
		for i := 0; i < pingCount; i++ {
			addr := fmt.Sprintf("%s://%s:%d", v.Protocol, v.DomainName, v.Port)
			req, err := http.NewRequest("GET", addr+"/ping", nil)
			if err != nil {
				log.Warnf(ctx, "unable to NewRequest, addr:%s, error: %s", addr, err)
				continue
			}
			resp, err := clint.Do(req)
			if err != nil {
				log.Warnf(ctx, "do ping failed, error: %s", err)
				continue
			}
			// 状态码 204 变更 read 跳出循环
			// 否则持续走完 PingCount 结束
			if resp.StatusCode == http.StatusNoContent {
				read = true
				break
			}
		}

		r.lock.RLock()
		if read == r.hostStatusInfo[v.DomainName].Read {
			r.lock.RUnlock()
			continue
		}
		r.lock.RUnlock()

		r.lock.Lock()
		r.hostStatusInfo[v.DomainName] = &influxdb.HostStatus{
			Read:           read,
			LastModifyTime: time.Now().Unix(),
		}
		r.lock.Unlock()
	}
}

func (r *Router) ReloadAllKey(ctx context.Context) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	for _, k := range influxdb.AllKey {
		err := r.loadRouter(ctx, k)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Router) RouterSubscribe(ctx context.Context) <-chan *goRedis.Message {
	r.lock.RLock()
	defer r.lock.RUnlock()
	return r.router.Subscribe(ctx)
}

// ReloadByKey 按 key 加载路由
func (r *Router) ReloadByKey(ctx context.Context, key string) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	return r.loadRouter(ctx, key)
}

// TimeSeriesClient 获取
func (r *Router) TimeSeriesClient(ctx context.Context, protocol, address string) remote.QueryTimeSeriesServiceClient {
	r.lock.RLock()
	defer r.lock.RUnlock()

	if r.endpointSet != nil {
		if cli, ok := r.endpointSet.endpoints[address]; ok {
			return cli.clients.timeSeries
		} else {
			er := r.endpointSet.getEndPointRef(ctx, protocol, address)
			if er != nil {
				r.endpointSet.endpoints[address] = er
				return er.clients.timeSeries
			}
		}
	}

	return nil
}

// GetInfluxDBHost 通过 tableID 获取 influxdb 查询实例
func (r *Router) GetInfluxDBHost(ctx context.Context, tagsKey []string, clusterName, db, measurement, condition string) (*influxdb.Host, error) {
	// 是否通过 tag 路由
	var (
		hostList []string
		index    = 0
		err      error

		unReadHost = make(map[string]struct{})

		hosts []*influxdb.Host

		readHost []string

		span oleltrace.Span
	)

	r.lock.RLock()
	defer r.lock.RUnlock()

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "get-influxdb-host")
	if span != nil {
		defer span.End()
	}

	trace.InsertStringIntoSpan("cluster-name", clusterName, span)

	if clusterHostList, ok := r.clusterInfo[clusterName]; ok {
		hostList = clusterHostList.HostList
		for _, h := range clusterHostList.UnreadableHostList {
			unReadHost[h] = struct{}{}
		}
	}

	trace.InsertStringSliceIntoSpan("all-host-list", hostList, span)
	trace.InsertStringIntoSpan("unread-host", fmt.Sprintf("%+v", unReadHost), span)

	if len(tagsKey) > 0 {
		hostList, err = r.getReadHostByTagsKey(ctx, tagsKey, clusterName, db, measurement, condition)
		if err != nil {
			return nil, err
		}

		trace.InsertStringIntoSpan("tags-key", fmt.Sprintf("%+v", tagsKey), span)
		trace.InsertStringSliceIntoSpan("tag-host-list", hostList, span)
	}

	for _, h := range hostList {
		// 该实例不可读
		if _, unRead := unReadHost[h]; unRead {
			continue
		}
		if v, hostOk := r.hostInfo[h]; hostOk {
			// 状态禁用直接跳过
			if v.Disabled {
				continue
			}

			// 有读状态才判断，如果没有读状态，则默认可读，防止影响原数据
			if s, statusOk := r.hostStatusInfo[v.DomainName]; statusOk {
				// 不可读的状态直接跳过
				if !s.Read {
					continue
				}
			}

			hosts = append(hosts, v)
			readHost = append(readHost, h)
		}
	}

	logInfo := fmt.Sprintf(
		"tagKey: %v, clusterName: %s, db: %s, measurement: %s, condition: %s",
		tagsKey, clusterName, db, measurement, condition,
	)

	if len(hosts) == 0 {
		return nil, fmt.Errorf("no influxdb host can read, %s", logInfo)
	}

	if len(hosts) > 1 {
		k := fmt.Sprintf("%v", hostList)

		hostMapLock.Lock()
		if _, ok := hostMapInc[k]; !ok {
			hostMapInc[k] = 0
		}
		hostMapInc[k]++
		index = int(math.Mod(float64(hostMapInc[k]), float64(len(hosts))))

		trace.InsertStringIntoSpan("host-map-inc", fmt.Sprintf("%+v", hostMapInc), span)
		trace.InsertIntIntoSpan("host-map-index", index, span)
		trace.InsertStringSliceIntoSpan("read-host-list", readHost, span)

		hostMapLock.Unlock()
	}

	if index < len(hosts) {
		trace.InsertStringIntoSpan("return-host", hosts[index].DomainName, span)
		return hosts[index], nil
	} else {
		return nil, fmt.Errorf("backend index is error: %d > %d, %s", index, len(hosts), logInfo)
	}
}

func (r *Router) Stop() error {
	r.wg.Wait()
	r.lock.Lock()
	defer r.lock.Unlock()

	if r.router != nil {
		err := r.router.Close()
		if err != nil {
			return err
		}
		r.router = nil
	}
	if r.endpointSet != nil {
		r.endpointSet.Close()
		r.endpointSet = nil
	}

	if r.cancelFunc != nil {
		r.cancelFunc()
	}

	return nil
}

func (r *Router) Print(ctx context.Context, reload bool) string {
	r.lock.RLock()
	defer r.lock.RUnlock()

	var res string
	if reload {
		res += fmt.Sprintln("reload")
		res += fmt.Sprintln("----------------------------------------")
		for _, k := range influxdb.AllKey {
			err := r.loadRouter(ctx, k)
			if err != nil {
				log.Errorf(ctx, err.Error())
			}
		}
	}

	var s []byte
	res += fmt.Sprintln("clusterInfo")
	for k, v := range r.clusterInfo {
		s, _ = json.Marshal(v)
		res += fmt.Sprintf("%s => %s\n", k, s)
	}

	res += fmt.Sprintln("----------------------------------------")

	res += fmt.Sprintln("hostInfo")
	for k, v := range r.hostInfo {
		s, _ = json.Marshal(v)
		res += fmt.Sprintf("%s => %s\n", k, s)
	}
	res += fmt.Sprintln("----------------------------------------")

	res += fmt.Sprintln("tagInfo")
	for k, v := range r.tagInfo {
		s, _ = json.Marshal(v)
		res += fmt.Sprintf("%s => %s\n", k, s)
	}
	res += fmt.Sprintln("----------------------------------------")

	res += fmt.Sprintln("proxyInfo")
	for k, v := range r.proxyInfo {
		s, _ = json.Marshal(v)
		res += fmt.Sprintf("%s => %s\n", k, s)
	}
	res += fmt.Sprintln("----------------------------------------")

	res += fmt.Sprintln("queryRouterInfo")
	for k, v := range r.queryRouterInfo {
		s, _ = json.Marshal(v)
		res += fmt.Sprintf("%s => %s\n", k, s)
	}
	res += fmt.Sprintln("----------------------------------------")

	res += fmt.Sprintln("hostStatusInfo")
	for k, v := range r.hostStatusInfo {
		s, _ = json.Marshal(v)
		res += fmt.Sprintf("%s => %s\n", k, s)
	}
	res += fmt.Sprintln("----------------------------------------")

	res += fmt.Sprintln("endpoints")
	for k, v := range r.endpointSet.endpoints {
		res += fmt.Sprintf("%s => %s\n", v.protocol, k)
	}

	return res
}

func (r *Router) loadRouter(ctx context.Context, key string) error {
	var (
		clusterInfo     influxdb.ClusterInfo
		hostInfo        influxdb.HostInfo
		tagInfo         influxdb.TagInfo
		proxyInfo       influxdb.ProxyInfo
		queryRouterInfo influxdb.QueryRouterInfo
		err             error
	)

	if r.router == nil {
		return fmt.Errorf("influxdb router is none")
	}

	switch key {
	case influxdb.ClusterInfoKey:
		clusterInfo, err = r.router.GetClusterInfo(ctx)
		if err == nil {
			r.clusterInfo = clusterInfo
		}
	case influxdb.HostInfoKey:
		hostInfo, err = r.router.GetHostInfo(ctx)
		if err == nil {
			r.hostInfo = hostInfo
			r.endpointSet.Update(ctx)

			// 更新 hostInfo 信息后重新初始化 hostStatusInfo
			r.hostStatusInfo = make(influxdb.HostStatusInfo, len(r.hostInfo))
			for _, h := range r.hostInfo {
				r.hostStatusInfo[h.DomainName] = &influxdb.HostStatus{Read: true}
			}
		}
	case influxdb.TagInfoKey:
		tagInfo, err = r.router.GetTagInfo(ctx)
		if err == nil {
			r.tagInfo = tagInfo
		}
	case influxdb.ProxyKey:
		proxyInfo, err = r.router.GetProxyInfo(ctx)
		if err == nil {
			r.proxyInfo = proxyInfo
		}
	case influxdb.QueryRouterInfoKey:
		queryRouterInfo, err = r.router.GetQueryRouterInfo(ctx)
		if err == nil {
			for k, v := range queryRouterInfo {
				var value = float64(0)
				if v.VmTableId != "" {
					value = 1
				}

				metric.ResultTableInfoSet(
					ctx, value, k, v.BkBizId, v.DataId, v.MeasurementType, v.VmTableId,
					v.BcsClusterId, fmt.Sprintf("%v", v.IsInfluxdbDisabled),
				)
			}
			r.queryRouterInfo = queryRouterInfo
		}
	}
	return err
}

func (r *Router) GetProxyByTableID(tableId, field string, isProxy bool) (*influxdb.Proxy, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()

	route := strings.Split(tableId, ".")
	if len(route) != 2 {
		return nil, fmt.Errorf("tableid format is wrong %s", tableId)
	}

	var ckList []string
	if isProxy {
		// 判断是否需要路由
		ckList = []string{
			fmt.Sprintf("%s.%s", route[0], field),
			tableId,
			fmt.Sprintf("%s.__default__", route[0]),
		}
	} else {
		ckList = []string{
			tableId,
		}
	}

	for _, ck := range ckList {
		if v, ok := r.proxyInfo[ck]; ok {
			return v, nil
		}
	}

	return nil, fmt.Errorf("influxdb proxy router is empty, with %s %s", tableId, field)
}

func (r *Router) GetQueryRouter(tableId string) (*influxdb.QueryRouter, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	if routerInfo, ok := r.queryRouterInfo[tableId]; ok {
		return routerInfo, nil
	} else {
		return nil, fmt.Errorf("tableid(%s) is not exists in Router.queryRouterInfo", tableId)
	}

}

func GetTagRouter(ctx context.Context, tagsKey []string, condition string) (string, error) {
	if len(tagsKey) == 0 || condition == "" {
		return "", nil
	}

	var (
		span oleltrace.Span
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "get-tag-values")
	if span != nil {
		defer span.End()
	}

	// 解析 where 中的条件
	tags, err := metadata.ParseCondition(condition)
	if err != nil {
		return "", err
	}

	trace.InsertStringIntoSpan("condition", condition, span)
	trace.InsertStringIntoSpan("condition-tags", fmt.Sprintf("%+v", tags), span)

	// 判断是否有 tagKey
	var buf bytes.Buffer
	checkRepeat := make(map[string]bool)
	count := 0

	for _, key := range tagsKey {
		for _, tag := range tags {
			// 获取条件里面路由的key和value
			if string(tag.Key) == key {
				// 特殊情况下，会有该维度的重复条件，所以这里进行了去重
				if _, ok := checkRepeat[key]; !ok {
					checkRepeat[key] = true
					if count != 0 {
						buf.WriteString(DivideSymbol)
					}
					count++
					buf.Write(tag.Key)
					buf.WriteString(EqualSymbol)
					buf.Write(tag.Value)
				}
			}
		}
	}
	return buf.String(), nil
}

// getReadHostByTagsKey 判断标签路由信息
func (r *Router) getReadHostByTagsKey(ctx context.Context, tagsKey []string, clusterName, db, measurement, condition string) ([]string, error) {
	// 只有 tagsKey 不为空才进行判断
	if len(tagsKey) == 0 {
		return nil, nil
	}

	var (
		span oleltrace.Span
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "get-read-host-by-tags-key")
	if span != nil {
		defer span.End()
	}

	allHostList := make(map[string]struct{})
	if cls, ok := r.clusterInfo[clusterName]; ok {
		for _, h := range cls.HostList {
			allHostList[h] = struct{}{}
		}
	}

	trace.InsertStringIntoSpan("all-host-list", fmt.Sprintf("%+v", allHostList), span)

	// 判断是否有 tagKey
	var buf bytes.Buffer
	buf.WriteString(clusterName + "/" + db + "/" + measurement + "/")

	tagRouter, err := GetTagRouter(ctx, tagsKey, condition)
	if err != nil {
		return nil, err
	}
	buf.WriteString(tagRouter)
	trace.InsertStringIntoSpan("check-tag-key", buf.String(), span)

	var (
		tagHostList []string
		hostList    []string
	)

	trace.InsertStringIntoSpan("check-tag-key", buf.String(), span)

	// 判断是否命中tag条件
	if tag, ok := r.tagInfo[buf.String()]; ok {
		tagHostList = tag.HostList

		trace.InsertStringIntoSpan("tag-info-list-key", fmt.Sprintf("%+v", tagHostList), span)
	} else {
		// 使用默认配置
		defaultTagKey := fmt.Sprintf("%s/%s", clusterName, DefaultTagKey)
		if tag, ok = r.tagInfo[defaultTagKey]; ok {
			tagHostList = tag.HostList

			trace.InsertStringIntoSpan("tag-info-default-key", defaultTagKey, span)
			trace.InsertStringIntoSpan("tag-info-list-default", fmt.Sprintf("%+v", tagHostList), span)
		} else {
			return nil, fmt.Errorf("default tag is empty: %v with: %s", r.tagInfo, defaultTagKey)
		}
	}

	// 跟集群的host做相交操作
	for _, h := range tagHostList {
		if _, ok := allHostList[h]; ok {
			hostList = append(hostList, h)
		}
	}

	trace.InsertStringIntoSpan("return-host-list", fmt.Sprintf("%+v", hostList), span)

	if len(hostList) == 0 {
		return nil, fmt.Errorf("empty influxdb host in %s, all backend: %+v", buf.String(), allHostList)
	}

	return hostList, nil
}
