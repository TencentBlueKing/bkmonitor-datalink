// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type schedulerHelper struct {
	done        chan struct{}
	conf        define.Configuration
	client      ClientAPI
	balanceConf *define.BalanceConfig
	deleted     map[string]int
	deletedMut  sync.Mutex
}

var SchedulerHelper = &schedulerHelper{
	conf: config.Configuration, done: make(chan struct{}, 1),
	deleted: map[string]int{},
}

// connect 懒加载建立链接
func (sh *schedulerHelper) connect() error {
	var err error
	if sh.client == nil {
		sh.client, err = NewConsulAPIFromConfig(config.Configuration)
	}

	return err
}

func (sh *schedulerHelper) Close() {
	if sh.client != nil {
		sh.client = nil
	}
	sh.done <- struct{}{}
}

func (sh *schedulerHelper) ForceGetConf() define.BalanceConfig {
	sh.syncConf()
	return sh.GetConf()
}

func (sh *schedulerHelper) GetConf() define.BalanceConfig {
	if sh.balanceConf != nil {
		return *sh.balanceConf
	}
	return define.DefaultBalanceConfig
}

func (sh *schedulerHelper) SyncConf() {
	tk := time.NewTicker(time.Minute)
	go func() {
		for {
			select {
			case <-tk.C:
				sh.syncConf()
			case <-sh.done:
				tk.Stop()
				return
			}
		}
	}()
}

func (sh *schedulerHelper) syncConf() {
	if err := sh.connect(); err != nil {
		logging.Errorf("failed to connect consul, err: %v", err)
		return
	}

	balancePath := utils.ResolveUnixPaths(sh.conf.GetString(ConfKeyServicePath), "schedule", "balance_conf")
	ctx, cancel := context.WithTimeout(context.Background(), define.DefaultConsulTimeout)
	defer cancel()

	pair, _, err := sh.client.KV().Get(balancePath, NewQueryOptions(ctx))
	if err != nil {
		logging.Errorf("failed to get consul key '%s', err: %v", balancePath, err)
		return
	}

	// 路径不存在时恢复默认设置
	if pair == nil {
		logging.Infof("empty consul key '%s'", balancePath)
		sh.balanceConf = nil
		return
	}

	var bc define.BalanceConfig
	if err := json.Unmarshal(pair.Value, &bc); err != nil {
		logging.Errorf("failed to unmarshal value, err: %v", err)
		return
	}

	sh.balanceConf = &bc
}

// Flow 查出所有 dataid 的流量列表 但不包含 dataid 类型
func (sh *schedulerHelper) Flow() (define.FlowItems, error) {
	if err := sh.connect(); err != nil {
		return nil, err
	}
	if err := sh.Detect(); err != nil {
		return nil, err
	}

	nodes, err := sh.LivingNodes()
	if err != nil {
		return nil, err
	}

	flowPath := utils.ResolveUnixPaths(sh.conf.GetString(ConfKeyServicePath), "flow", sh.conf.GetString(ConfKeyServiceName))
	ctx, cancel := context.WithTimeout(context.Background(), define.DefaultConsulTimeout)
	defer cancel()

	flowKvs, _, err := sh.client.KV().List(flowPath, NewQueryOptions(ctx))
	if err != nil {
		return nil, err
	}

	var ret define.FlowItems
	for _, kv := range flowKvs {
		var found bool
		for _, node := range nodes {
			if strings.Contains(kv.Key, node) {
				found = true
				break
			}
		}

		if !found {
			continue
		}
		flowItem := extractFlowDetailed(kv.Key, string(kv.Value))
		if flowItem == nil {
			continue
		}

		_, etl := pipeline.GetPipelineMeta(flowItem.DataID)
		flowItem.Flow = int(float64(flowItem.Flow) * pipeline.GetPipelineWeight(etl)) //  对流量进行放大或者缩小
		ret = append(ret, *flowItem)
	}
	sort.Sort(ret)

	return ret, nil
}

func (sh *schedulerHelper) LivingNodes() ([]string, error) {
	sessionPath := utils.ResolveUnixPaths(sh.conf.GetString(ConfKeyServicePath), "session/")
	ctx, cancel := context.WithTimeout(context.Background(), define.DefaultConsulTimeout)
	defer cancel()

	kvs, _, err := sh.client.KV().Keys(sessionPath, "/", NewQueryOptions(ctx))
	if err != nil {
		return nil, err
	}
	// bk_bkmonitorv3_enterprise_production/service/v1/default/session/bkmonitorv3-2604497288/
	// bk_bkmonitorv3_enterprise_production/service/v1/default/session/bkmonitorv3-2604497289/

	var nodes []string
	for _, kv := range kvs {
		kv = strings.TrimSuffix(kv, "/")
		split := strings.Split(kv, "/")
		if len(split) <= 0 {
			continue
		}
		node := split[len(split)-1] // bkmonitorv3-2604497288,bkmonitorv3-2604497289
		nodes = append(nodes, node)
	}

	return nodes, nil
}

func (sh *schedulerHelper) Detect() error {
	if err := sh.detect(); err != nil {
		return err
	}

	sh.deletedMut.Lock()
	defer sh.deletedMut.Unlock()

	for k, v := range sh.deleted {
		if v >= 5 {
			delete(sh.deleted, k)
			logging.Infof("deleted unflow dataid '%s'", k)
			if err := sh.deleteFlow(k); err != nil {
				logging.Errorf("shadow delete data_id item %s error %+v", k, err)
			}
		}
	}

	return nil
}

func (sh *schedulerHelper) deleteFlow(p string) error {
	ctx, cancel := context.WithTimeout(context.Background(), define.DefaultConsulTimeout)
	defer cancel()
	_, err := sh.client.KV().DeleteTree(p, NewWriteOptions(ctx))
	return err
}

func (sh *schedulerHelper) detect() error {
	// 获取存在的 dataid keys
	dataidPath := utils.ResolveUnixPaths(sh.conf.GetString(ConfKeyServicePath), "data_id", sh.conf.GetString(ConfKeyServiceName))
	ctx, cancel := context.WithTimeout(context.Background(), define.DefaultConsulTimeout)
	defer cancel()

	dataidKvs, _, err := sh.client.KV().List(dataidPath, NewQueryOptions(ctx))
	if err != nil {
		return err
	}
	dataidKeyMap := make(map[string]bool)
	for _, k := range dataidKvs {
		p := sh.getDataId(k.Key, "data_id")
		if p != "" {
			dataidKeyMap[p] = true
		}
	}

	// 获取存在的 flow keys
	flowPath := utils.ResolveUnixPaths(sh.conf.GetString(ConfKeyServicePath), "flow", sh.conf.GetString(ConfKeyServiceName))
	ctx, cancel = context.WithTimeout(context.Background(), define.DefaultConsulTimeout)
	defer cancel()

	flowKvs, _, err := sh.client.KV().List(flowPath, NewQueryOptions(ctx))
	if err != nil {
		return err
	}
	flowKeyMap := make(map[string]bool)
	for _, k := range flowKvs {
		p := sh.getDataId(k.Key, "flow")
		if p != "" {
			flowKeyMap[p] = true
		}
	}

	sh.deletedMut.Lock()
	for flowKey := range flowKeyMap {
		// 如果 flow key 存在，但 dataid 已经被删除了
		p := utils.ResolveUnixPaths(sh.conf.GetString(ConfKeyServicePath), "flow", flowKey)
		if !dataidKeyMap[flowKey] {
			sh.deleted[p]++
			continue
		}

		// 如果存在的话 权重--
		// 避免由于服务重启或者其他原因导致 flow 暂时没被发现
		if sh.deleted[p] > 0 {
			sh.deleted[p]--
		}
	}
	sh.deletedMut.Unlock()

	return nil
}

func (sh *schedulerHelper) getDataId(s string, typ string) string {
	split := strings.Split(s, "/")
	if len(split) <= 3 {
		return ""
	}
	if split[len(split)-3] != typ {
		return ""
	}

	// bk_bkmonitorv3_enterprise_production/service/v1/default/flow/bkmonitorv3-3192286418/1572994
	p := split[len(split)-2] + "/" + split[len(split)-1] // bkmonitorv3-3192286418/1572994
	return p
}

// List 用于使用 `transfer flow` 查看流量情况
func (sh *schedulerHelper) List() (define.FlowItems, error) {
	flow, err := sh.Flow()
	if err != nil {
		return nil, err
	}

	// 查询 data_id 路径只是为了获取数据类型 `log/time_series/...`
	dataidPath := utils.ResolveUnixPaths(sh.conf.GetString(ConfKeyServicePath), "data_id", sh.conf.GetString(ConfKeyServiceName))
	ctx, cancel := context.WithTimeout(context.Background(), define.DefaultConsulTimeout)
	defer cancel()

	dataidKvs, _, err := sh.client.KV().List(dataidPath, NewQueryOptions(ctx))
	if err != nil {
		return nil, err
	}

	labels := make(map[int]string)
	for _, kv := range dataidKvs {
		e := DispatchItemConf{Pair: kv}
		var kvpair KVPair
		if err = json.Unmarshal(kv.Value, &kvpair); err != nil {
			logging.Errorf("failed to marshal dataid kv, err:%v", err)
			continue
		}

		if err = json.Unmarshal(kvpair.Value, &e.Config); err != nil {
			logging.Errorf("failed to marshal dataid detailed, err:%v", err)
			continue
		}

		labels[e.Config.DataID] = e.Config.TypeLabel
	}

	for i := 0; i < len(flow); i++ {
		flow[i].Type = labels[flow[i].DataID]
	}

	return flow, nil
}

func extractFlowDetailed(p, flow string) *define.FlowItem {
	const splitLen = 7

	splits := strings.Split(p, "/")
	if len(splits) < splitLen {
		logging.Errorf("failed to split dataid flow, path: %s", p)
		return nil
	}

	i, err := strconv.Atoi(flow)
	if err != nil {
		logging.Errorf("failed to get dataid flow, value: %s", flow)
		return nil
	}

	// bk_bkmonitorv3_enterprise_production/service/v1/default/flow/bkmonitorv3-2604497288/1001
	// 流量信息：${namespace}/service/${version}/${cluster}/flow/${service}/${dataid}
	const (
		serviceIdx = 5
		clusterIdx = 3
		dataidIdx  = 6
	)

	dataidStr := splits[dataidIdx]
	dataid, err := strconv.Atoi(dataidStr)
	if err != nil {
		logging.Errorf("failed to get dataid, value: %s, err: %v", dataidStr, err)
		return nil
	}

	return &define.FlowItem{
		DataID:  dataid,
		Cluster: splits[clusterIdx],
		Service: splits[serviceIdx],
		Path:    p,
		Flow:    i,
	}
}
