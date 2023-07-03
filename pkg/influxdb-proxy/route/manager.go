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
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

var (
	routeManager   *Manager
	moduleName     = "route_manager"
	defaultCluster = "_default"
	DefaultTable   = "__default__"
)

// Init :
var Init = func(outCtx context.Context) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("route init start")
	var err error
	routeManager, err = newManager(outCtx)
	if err != nil {
		flowLog.Errorf("init route failed")
		return err
	}
	flowLog.Debugf("route init done")
	return nil
}

// Reload 重载路由
var Reload = func(outCtx context.Context) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("route reload start")
	var err error
	if routeManager != nil {
		err = routeManager.Stop()
		if err != nil {
			flowLog.Errorf("stop route manager failed")
			return err
		}
	}
	flowLog.Tracef("route stop pre Manger done")
	routeManager, err = newManager(outCtx)
	if err != nil {
		flowLog.Errorf("reinit route manager failed")
		return err
	}
	flowLog.Debugf("route reload done")
	return nil
}

// GetClusterByRoute :
var GetClusterByRoute = func(flow uint64, path string) (cluster.Cluster, error) {
	return routeManager.GetClusterByRoute(flow, path)
}

// GetClusterByName :
var GetClusterByName = func(flow uint64, name string) (cluster.Cluster, error) {
	return routeManager.GetClusterByName(flow, name)
}

// Refresh :
var Refresh = func() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("route refresh start")
	info, err := consul.GetAllRoutesData()
	if err != nil {
		flowLog.Errorf("refresh route failed,error:%s", err)
		return err
	}
	flowLog.Tracef("route get route data done")
	routeInfoMap := make(map[string]*Info)
	for key, value := range info {
		routeInfoMap[key] = &Info{
			Cluster:      value.Cluster,
			PartitionTag: value.PartitionTag,
		}
	}

	err = routeManager.Refresh(routeInfoMap)
	if err != nil {
		flowLog.Errorf("refresh route failed,error:%s", err)
		return err
	}
	flowLog.Debugf("route refresh done")
	return nil
}

// Print :
var Print = func() string {
	return routeManager.Print()
}

// Backup :
var Backup = func() {
	routeManager.Backup()
}

// Recover :
var Recover = func() error {
	return routeManager.Recover()
}

// Manager :
type Manager struct {
	ctx            context.Context
	cancelFunc     context.CancelFunc
	routeMap       map[string]cluster.Cluster
	tagMap         map[string][]string
	usingRouteInfo map[string]*Info

	backupRouteMap  map[string]cluster.Cluster
	backupRouteInfo map[string]*Info
	lock            sync.RWMutex
}

// Print 打印信息
func (m *Manager) Print() string {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	m.lock.RLock()
	flowLog.Tracef("get Rlock")
	defer func() {
		m.lock.RUnlock()
		flowLog.Tracef("release Rlock")
	}()
	var result string
	for k, v := range m.routeMap {
		result = result + fmt.Sprintf("route:%s,cluster:%s", k, v.GetName())
		if tags, ok := m.tagMap[k]; ok && len(tags) != 0 {
			result = fmt.Sprintf("%s,tags:%v\n", result, tags)
		} else {
			result = fmt.Sprintf("%s\n", result)
		}
	}
	return result
}

// newManager :
func newManager(outCtx context.Context) (*Manager, error) {
	m := new(Manager)
	m.ctx, m.cancelFunc = context.WithCancel(outCtx)
	m.usingRouteInfo = make(map[string]*Info)
	return m, nil
}

// Stop :
func (m *Manager) Stop() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	m.lock.Lock()
	flowLog.Tracef("get lock")
	defer func() {
		m.lock.Unlock()
		flowLog.Tracef("release lock")
	}()
	if m.cancelFunc != nil {
		m.cancelFunc()
	}
	flowLog.Debugf("route stop done")
	return nil
}

func (m *Manager) isTagRoute(path string) bool {
	isTagRoute := false
	if partitionTags, ok := m.tagMap[path]; ok && len(partitionTags) > 0 {
		isTagRoute = true
	}
	return isTagRoute
}

// GetClusterByRoute 根据路由获取集群
func (m *Manager) GetClusterByRoute(flow uint64, path string) (cluster.Cluster, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
	})
	flowLog.Debugf("called,path:%s", path)
	m.lock.RLock()
	flowLog.Tracef("get Rlock")
	defer func() {
		m.lock.RUnlock()
		flowLog.Tracef("release Rlock")
	}()
	var cluster cluster.Cluster
	var ok bool
	originPath := path
	flowLog.Tracef("route start match,path:%s", path)
	// 拼接成指定路由格式,匹配对应的集群
	// routeMap直接映射了实际集群，所以这里直接取值返回
	if cluster, ok = m.routeMap[path]; ok {
		return cluster, nil
	}

	// 精确匹配失败，尝试以默认DB路由取一次
	list := strings.Split(path, ".")
	if len(list) == 2 {
		// 拿db做路由再查一次
		db := list[0]
		path = db + "." + DefaultTable
		if cluster, ok = m.routeMap[path]; ok {
			return cluster, nil
		}
	}

	// 最后取全局默认路由，是针对非监控场景的方案
	cluster, err := m.getDefaultCluster(flowLog)
	if err == nil {
		return cluster, nil
	}
	flowLog.Errorf("unable to get cluster by input path:%s and default DB path:%s,cluster match failed,error:%s", originPath, path, err)
	return nil, ErrGetClusterFailed
}

// GetClusterByName 根据集群名获取集群
func (m *Manager) GetClusterByName(flow uint64, name string) (cluster.Cluster, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
	})
	flowLog.Debugf("called,name:%s", name)
	m.lock.RLock()
	flowLog.Tracef("get Rlock")
	defer func() {
		m.lock.RUnlock()
		flowLog.Tracef("release Rlock")
	}()
	item, err := m.getClusterFromManager(name, flowLog)
	if err != nil {
		flowLog.Debugf("get cluster by route failed,use %s cluster instead,error:%s", defaultCluster, err)
		item, err = m.getDefaultCluster(flowLog)
		if err != nil {
			flowLog.Errorf("unable to get %s cluster,cluster match failed,error:%s", defaultCluster, err)
			return nil, ErrGetClusterFailed
		}
	}
	flowLog.Debugf("match done")
	return item, nil
}

func (m *Manager) getDefaultCluster(flowLog *logging.Entry) (cluster.Cluster, error) {
	cluster, err := cluster.GetCluster(defaultCluster)
	if err != nil {
		flowLog.Errorf("get default cluster failed,error:%s", err)
		return nil, err
	}
	return cluster, nil
}

func (m *Manager) getClusterFromManager(name string, flowLog *logging.Entry) (cluster.Cluster, error) {
	flowLog.Debugf("called,name:%s", name)
	// 根据集群名从cluster的manage中获取集群实体
	cluster, err := cluster.GetCluster(name)
	if err != nil {
		flowLog.Errorf("get cluster by name failed,error:%s", err)
		return nil, err
	}
	flowLog.Debugf("done")
	return cluster, nil
}

func (m *Manager) logStatus() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	m.lock.RLock()
	flowLog.Tracef("route manager logStatus:get Rlock")
	defer func() {
		m.lock.RUnlock()
		flowLog.Tracef("route manager logStatus:release Rlock")
	}()
	flowLog.Tracef("status report:current running route num:%d", len(m.routeMap))
	for k, v := range m.routeMap {
		flowLog.Tracef("status report:route->[%s],cluster:%s", k, v.GetName())
	}
	flowLog.Tracef("done")
}

// Refresh :
func (m *Manager) Refresh(info map[string]*Info) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	err := m.refreshRoutes(info)
	if err != nil {
		return err
	}
	m.logStatus()
	flowLog.Debugf("cluster manager Refresh done")
	return nil
}

// refreshRoutes 刷新表映射信息,直接锁住然后替换
func (m *Manager) refreshRoutes(data map[string]*Info) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("route start refresh")
	routeMap := make(map[string]cluster.Cluster)
	tagMap := make(map[string][]string)
	for k, v := range data {
		clusterName := v.Cluster
		cluster, err := cluster.GetCluster(clusterName)
		if err != nil {
			flowLog.Errorf("cluster->[%s] not found, please check backend cluster of route from metadata", clusterName)
			continue
		}
		routeMap[k] = cluster
		tagMap[k] = v.PartitionTag
		flowLog.Tracef("route->[%s] added,cluster:%s", k, cluster)
	}
	// 替换旧信息
	m.lock.Lock()
	flowLog.Tracef("route manager refreshRoutes:get lock")
	defer func() {
		m.lock.Unlock()
		flowLog.Tracef("route manager refreshRoutes:release lock")
	}()
	m.usingRouteInfo = data
	m.routeMap = routeMap
	m.tagMap = tagMap
	flowLog.Debugf("route refresh finished")
	return nil
}

// Backup 备份信息
func (m *Manager) Backup() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	m.lock.RLock()
	flowLog.Debugf("get lock")
	defer func() {
		m.lock.RUnlock()
		flowLog.Debugf("release lock")
	}()

	m.backupRouteMap = make(map[string]cluster.Cluster)
	m.backupRouteInfo = make(map[string]*Info)
	for k, v := range m.routeMap {
		m.backupRouteMap[k] = v
	}
	for k, v := range m.usingRouteInfo {
		m.backupRouteInfo[k] = v
	}
}

// Recover 恢复备份的信息
func (m *Manager) Recover() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	m.lock.Lock()
	flowLog.Debugf("get lock")
	defer func() {
		m.lock.Unlock()
		flowLog.Debugf("release lock")
	}()
	if m.backupRouteMap == nil || m.backupRouteInfo == nil {
		return ErrBackupIsNil
	}
	m.routeMap = m.backupRouteMap
	m.usingRouteInfo = m.backupRouteInfo
	m.backupRouteMap = nil
	m.backupRouteInfo = nil
	return nil
}
