// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cluster

import (
	"context"
	"fmt"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

var (
	clusterManager *Manager
	moduleName     = "cluster"
)

// Init 初始化
var Init = func(outCtx context.Context) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("cluster manager init start")
	var err error
	clusterManager, err = newManager(outCtx)
	if err != nil {
		flowLog.Errorf("init cluster manager failed")
		return err
	}
	flowLog.Debugf("cluster manager inited")
	return nil
}

// GetCluster 获取名称对应的集群
var GetCluster = func(name string) (Cluster, error) {
	return clusterManager.GetCluster(name)
}

// Reload 重载backend，会清空当前所有列表然后初始化
var Reload = func(ctx context.Context) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	var err error
	if clusterManager != nil {
		err = clusterManager.Stop()
		if err != nil {
			flowLog.Errorf("stop cluster manager failed")
			return err
		}
	}

	clusterManager, err = newManager(ctx)
	if err != nil {
		flowLog.Errorf("reinit cluster manager failed")
		return err
	}
	flowLog.Debugf("done")
	return nil
}

// Refresh :
var Refresh = func() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	info, err := consul.GetAllClustersData()
	if err != nil {
		flowLog.Errorf("refresh cluster failed,error:%s", err)
		return err
	}
	clusterInfoMap := make(map[string]*Info)
	for key, value := range info {
		clusterInfoMap[key] = &Info{
			HostList:           value.HostList,
			UnReadableHostList: value.UnReadableHostList,
		}
	}
	err = clusterManager.Refresh(clusterInfoMap)
	if err != nil {
		flowLog.Errorf("refresh cluster failed,error:%s", err)
		return err
	}
	flowLog.Debugf("done")
	return nil
}

// Print :
var Print = func() string {
	return clusterManager.Print()
}

// Backup :
var Backup = func() {
	clusterManager.Backup()
}

// Recover :
var Recover = func() error {
	return clusterManager.Recover()
}

// Manager :
type Manager struct {
	ctx               context.Context
	cancelFunc        context.CancelFunc
	clusterMap        map[string]Cluster
	usingClusterInfo  map[string]*Info
	backupClusterMap  map[string]Cluster
	backupClusterInfo map[string]*Info
	lock              sync.RWMutex
}

// newManager :
func newManager(outCtx context.Context) (*Manager, error) {
	m := new(Manager)
	m.ctx, m.cancelFunc = context.WithCancel(outCtx)
	m.clusterMap = make(map[string]Cluster)
	m.usingClusterInfo = make(map[string]*Info)

	return m, nil
}

func (m *Manager) logStatus() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	m.lock.RLock()
	flowLog.Tracef("cluster manager logStatus:get Rlock")
	defer func() {
		m.lock.RUnlock()
		flowLog.Tracef("cluster manager logStatus:release Rlock")
	}()
	flowLog.Tracef("status report:current running cluster num:%d", len(m.clusterMap))
	for _, v := range m.clusterMap {
		flowLog.Infof("status report:cluster->[%s]", v)
	}
	flowLog.Tracef("done")
}

// Stop 停止
func (m *Manager) Stop() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	m.lock.Lock()
	flowLog.Tracef("cluster manager Stop:get lock")
	defer func() {
		m.lock.Unlock()
		flowLog.Tracef("cluster manager Stop:release lock")
	}()
	if m.cancelFunc != nil {
		m.cancelFunc()
	}
	flowLog.Debugf("done")
	return nil
}

// Refresh :
func (m *Manager) Refresh(info map[string]*Info) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	err := m.refreshCluster(info)
	if err != nil {
		return err
	}
	m.logStatus()
	flowLog.Debugf("done")
	return nil
}

// GetCluster 获取指定的集群
func (m *Manager) GetCluster(name string) (Cluster, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	m.lock.RLock()
	flowLog.Tracef("get Rlock")
	defer func() {
		m.lock.RUnlock()
		flowLog.Tracef("release Rlock")
	}()

	cluster, ok := m.clusterMap[name]
	if !ok {
		flowLog.Errorf("match cluster by name failed,cluster->[%s] not exist", name)
		return nil, ErrClusterNotExist
	}
	flowLog.Debugf("done")
	return cluster, nil
}

// Print 打印自身存储的信息
func (m *Manager) Print() string {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	m.lock.RLock()
	flowLog.Tracef("cluster manager Print:get Rlock")
	defer func() {
		m.lock.RUnlock()
		flowLog.Tracef("cluster manager Print:release Rlock")
	}()
	var result string
	for _, v := range m.clusterMap {
		result = result + fmt.Sprintf("%s\n", v)
	}
	flowLog.Tracef("done")
	return result
}

// refreshCluster 刷新集群信息,由于集群信息的长期持有仅为一个map，所以直接更新map即可
func (m *Manager) refreshCluster(data map[string]*Info) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	addMap := make(map[string]*Info)
	modMap := make(map[string]*Info)
	delMap := make(map[string]*Info)

	flowLog.Debugf("calculate cluster changed")
	// 增、改
	for name, info := range data {
		if preCluster, ok := m.usingClusterInfo[name]; !ok {
			addMap[name] = info
		} else {
			// 如果主机名相同，就进行比较,不同则进入修改列表
			if !info.Compare(preCluster) {
				modMap[name] = info
			}
		}
	}
	// 删
	for name, info := range m.usingClusterInfo {
		if _, ok := data[name]; !ok {
			delMap[name] = info
		}
	}

	if len(addMap) == 0 && len(modMap) == 0 && len(delMap) == 0 {
		flowLog.Tracef("cluster have no change")
		return nil
	}

	flowLog.Debugf("start to change cluster map")
	m.lock.Lock()
	flowLog.Tracef("get lock")
	defer func() {
		m.lock.Unlock()
		flowLog.Tracef("release lock")
	}()
	tempMap := make(map[string]Cluster, len(m.clusterMap))
	for k, v := range m.clusterMap {
		tempMap[k] = v
	}
	var hasError bool
	flowLog.Debugf("add cluster")
	// 对三个map进行处理
	for name, cluster := range addMap {
		err := m.addCluster(name, cluster)
		if err != nil {
			hasError = true
			flowLog.Errorf("add cluster->[%s] failed,error:%s", name, err)
			continue
		}
	}
	flowLog.Debugf("modify cluster")
	for name, cluster := range modMap {
		err := m.modifyCluster(name, cluster)
		if err != nil {
			hasError = true
			flowLog.Errorf("modify cluster->[%s] failed,error:%s", name, err)
			continue
		}
	}
	flowLog.Debugf("delete cluster")
	for name := range delMap {
		err := m.deleteCluster(name)
		if err != nil {
			hasError = true
			flowLog.Errorf("delete cluster->[%s] failed,error:%s", name, err)
			continue
		}
	}
	// 如果有错误，就恢复到原来的集群列表
	if hasError {
		flowLog.Errorf("get error when refreshing clusters,refresh canceled")
		m.clusterMap = tempMap
		return ErrRefreshFailed
	}

	// 替换旧信息
	m.usingClusterInfo = data
	flowLog.Debugf("done")
	return nil
}

func (m *Manager) addCluster(name string, clusterInfo *Info) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	item, err := m.makeCluster(name, clusterInfo)
	if err != nil {
		flowLog.Errorf("make cluster->[%s] failed,error:%s", name, err)
		return err
	}
	m.clusterMap[name] = item
	flowLog.Tracef("cluster->[%s] add finished", item)
	flowLog.Tracef("done")
	return nil
}

func (m *Manager) modifyCluster(name string, clusterInfo *Info) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	if cluster, ok := m.clusterMap[name]; ok {
		if err := cluster.Reset(name, clusterInfo.HostList, clusterInfo.UnReadableHostList); err != nil {
			flowLog.Errorf("cluster->[%s] modify failed,error:%s", cluster, err)
			return err
		}
		flowLog.Tracef("cluster->[%s] modify finished", cluster)
		flowLog.Tracef("done")
		return nil
	}
	flowLog.Errorf("modify cluster->[%s] failed,bacause it does not exist", name)
	return ErrClusterNotExist
}

func (m *Manager) deleteCluster(name string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	if item, ok := m.clusterMap[name]; ok {
		delete(m.clusterMap, name)
		flowLog.Tracef("cluster->[%s] delete finished", item)
		flowLog.Tracef("done")
		return nil
	}
	flowLog.Errorf("delete cluster->[%s] failed,bacause it does not exist", name)
	return ErrClusterNotExist
}

func (m *Manager) makeCluster(name string, clusterInfo *Info) (Cluster, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	// 将list转换为map，以提升效率
	unreadableHostMap := ConvertListToMap(clusterInfo.UnReadableHostList)
	backendList, _, err := backend.GetBackendList(clusterInfo.HostList)
	if err != nil {
		flowLog.Errorf("GetBackendList failed,error:%s", err)
		return nil, err
	}
	clusterFunc := GetClusterFunc("routecluster")
	item, err := clusterFunc(m.ctx, name, backendList, unreadableHostMap)
	if err != nil {
		flowLog.Errorf("generate cluster failed,error:%s", err)
		return nil, err
	}
	flowLog.Debugf("done")
	return item, nil
}

// Backup 将配置备份，用于之后可能的recover
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

	m.backupClusterMap = make(map[string]Cluster)
	m.backupClusterInfo = make(map[string]*Info)
	for k, v := range m.clusterMap {
		m.backupClusterMap[k] = v
	}
	for k, v := range m.usingClusterInfo {
		m.backupClusterInfo[k] = v
	}
}

// Recover 将配置回滚到备份状态
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
	if m.backupClusterMap == nil || m.backupClusterInfo == nil {
		return ErrBackupIsNil
	}

	m.clusterMap = m.backupClusterMap
	m.usingClusterInfo = m.backupClusterInfo
	m.backupClusterMap = nil
	m.backupClusterInfo = nil
	return nil
}
