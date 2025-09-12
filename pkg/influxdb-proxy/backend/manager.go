// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package backend

import (
	"context"
	"fmt"
	"sync"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// BackendManager 全局单例的backend管理器
var (
	BackendManager *Manager
	moduleName     = "backend"
)

// Init 初始化
var Init = func(outCtx context.Context) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	var err error
	BackendManager, err = newManager(outCtx)
	if err != nil {
		flowLog.Errorf("init backend manager failed,error:%s", err)
		return err
	}
	flowLog.Debugf("done")
	return nil
}

// GetBackend 获取backend
var GetBackend = func(name string) (Backend, error) {
	return BackendManager.GetBackend(name)
}

// GetBackendList 获取backend列表
var GetBackendList = func(nameList []string) ([]Backend, []string, error) {
	return BackendManager.GetBackendList(nameList)
}

// Reload 重载backend，会清空当前所有列表然后初始化
var Reload = func(ctx context.Context) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	var err error
	if BackendManager != nil {
		err = BackendManager.Stop()
		if err != nil {
			flowLog.Errorf("stop backend manager failed,error:%s", err)
			return err
		}
	}

	BackendManager, err = newManager(ctx)
	if err != nil {
		flowLog.Errorf("reinit backend manager failed,error:%s", err)
		return err
	}
	flowLog.Debugf("done")
	return nil
}

// Print :
var Print = func() string {
	return BackendManager.Print()
}

// Backup :
var Backup = func() {
	BackendManager.Backup()
}

// Recover :
var Recover = func() error {
	return BackendManager.Recover()
}

// Refresh 刷新主机数据
var Refresh = func() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	infoMap, err := consul.GetAllHostsData()
	if err != nil {
		flowLog.Errorf("refresh backend failed,error:%s", err)
		return err
	}
	hostInfoMap := make(map[string]*Info)
	for key, info := range infoMap {
		hostInfoMap[key] = &Info{
			Username:        info.Username,
			Password:        info.Password,
			DomainName:      info.DomainName,
			Port:            info.Port,
			Disabled:        info.Disabled,
			BackupRateLimit: info.BackupRateLimit,
			Protocol:        info.Protocol,
		}
	}
	err = BackendManager.Refresh(hostInfoMap)
	if err != nil {
		flowLog.Errorf("refresh backend failed,error:%s", err)
		return err
	}
	flowLog.Debugf("done")
	return nil
}

// Manager :
type Manager struct {
	ctx              context.Context
	cancelFunc       context.CancelFunc
	backendMap       map[string]Backend
	usingHostInfo    map[string]*Info
	backupBackendMap map[string]Backend
	backupHostInfo   map[string]*Info
	lock             sync.RWMutex
}

// NewManager 新建
func newManager(outCtx context.Context) (*Manager, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	m := new(Manager)
	m.ctx, m.cancelFunc = context.WithCancel(outCtx)
	m.backendMap = make(map[string]Backend)
	m.usingHostInfo = make(map[string]*Info)
	flowLog.Debugf("done")
	return m, nil
}

// Print 打印存储的配置信息
func (m *Manager) Print() string {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	m.lock.RLock()
	flowLog.Debugf("get Rlock")
	defer func() {
		m.lock.RUnlock()
		flowLog.Debugf("release Rlock")
	}()
	var result string
	for _, v := range m.backendMap {
		result = result + fmt.Sprintf("%s\n", v.String())
	}
	flowLog.Debugf("done")
	return result
}

// Stop 停止
func (m *Manager) Stop() error {
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
	if m.cancelFunc != nil {
		m.cancelFunc()
	}
	for _, v := range m.backendMap {
		v.Wait()
	}
	flowLog.Debugf("done")
	return nil
}

func (m *Manager) logStatus() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	m.lock.RLock()
	flowLog.Debugf("backend manager logStatus:get Rlock")
	defer func() {
		m.lock.RUnlock()
		flowLog.Debugf("backend manager logStatus:release Rlock")
	}()
	flowLog.Debugf("status report:current running backend num:%d", len(m.backendMap))
	for _, v := range m.backendMap {
		flowLog.Debugf("status report:backend->[%s]", v)
	}
	flowLog.Debugf("done")
}

// Refresh 刷新
func (m *Manager) Refresh(info map[string]*Info) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	err := m.refreshBackend(info)
	if err != nil {
		return err
	}
	m.logStatus()
	flowLog.Debugf("done")
	return nil
}

// GetBackend 获取指定的backend
func (m *Manager) GetBackend(name string) (Backend, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	m.lock.RLock()
	flowLog.Debugf("backend manager GetBackend:get Rlock")
	defer func() {
		m.lock.RUnlock()
		flowLog.Debugf("backend manager GetBackend:release Rlock")
	}()
	backend, ok := m.backendMap[name]
	if !ok {
		flowLog.Errorf("match backend by name->[%s] failed", name)
		return nil, ErrBackendNotExist
	}
	flowLog.Debugf("done")
	return backend, nil
}

// GetBackendList 获取指定的backend列表
func (m *Manager) GetBackendList(nameList []string) ([]Backend, []string, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	m.lock.RLock()
	flowLog.Debugf("backend manager GetBackendList:get Rlock")
	defer func() {
		m.lock.RUnlock()
		flowLog.Debugf("backend manager GetBackendList:release Rlock")
	}()
	var err error
	backendList := make([]Backend, 0)
	emptyList := make([]string, 0)
	for _, name := range nameList {
		backend, ok := m.backendMap[name]
		if !ok {
			emptyList = append(emptyList, name)
			continue
		}

		// 判断 backend 是否禁用
		if backend.Disabled() {
			continue
		}

		backendList = append(backendList, backend)
	}

	if len(emptyList) > 0 {
		err = ErrBackendNotExistInList
	}
	flowLog.Debugf("done")
	return backendList, emptyList, err
}

// refreshBackend 刷新主机信息
func (m *Manager) refreshBackend(data map[string]*Info) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	addMap := make(map[string]*Info)
	modMap := make(map[string]*Info)
	delMap := make(map[string]*Info)

	flowLog.Debugf("check change of backends")
	// 增、改
	for name, host := range data {
		if preHost, ok := m.usingHostInfo[name]; !ok {
			addMap[name] = host
		} else {
			// 如果主机名相同，就进行比较,不同则进入修改列表
			if !host.Compare(preHost) {
				modMap[name] = host
			}
		}
	}
	// 删
	for name, host := range m.usingHostInfo {
		if _, ok := data[name]; !ok {
			delMap[name] = host
		}
	}

	if len(addMap) == 0 && len(modMap) == 0 && len(delMap) == 0 {
		flowLog.Debugf("backend have no change")
		return nil
	}
	flowLog.Debugf("start to change backend map")
	m.lock.Lock()
	flowLog.Debugf("get lock")
	defer func() {
		m.lock.Unlock()
		flowLog.Debugf("release lock")
	}()
	tempMap := make(map[string]Backend, len(m.backendMap))
	for k, v := range m.backendMap {
		tempMap[k] = v
	}
	var hasError bool
	flowLog.Debugf("start add Map")
	// 对三个map进行处理
	for name, host := range addMap {
		err := m.addBackend(name, host)
		if err != nil {
			hasError = true
			flowLog.Errorf("add backend->[%s] failed,error:%s", name, err)
			continue
		}
	}
	flowLog.Debugf("start mod Map")
	for name, host := range modMap {
		err := m.modifyBackend(name, host)
		if err != nil {
			hasError = true
			flowLog.Errorf("modify backend->[%s] failed,error:%s", name, err)
			continue
		}
	}
	flowLog.Debugf("start delete Map")
	for name := range delMap {
		err := m.deleteBackend(name)
		if err != nil {
			hasError = true
			flowLog.Errorf("delete backend->[%s] failed,error:%s", name, err)
			continue
		}
	}
	if hasError {
		flowLog.Errorf("get error when refreshing backends,refresh canceled")
		m.backendMap = tempMap
		return ErrRefreshFailed
	}

	// 替换旧信息
	m.usingHostInfo = data
	flowLog.Debugf("done")
	return nil
}

func (m *Manager) addBackend(name string, hostInfo *Info) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	preBackend, err := m.makeBackend(name, hostInfo)
	// 将此backend添加到全体backend中
	if err != nil {
		// 异常时，关闭 preBackend
		preBackend.Close()
		flowLog.Errorf("make backend->[%s] failed", name)
		return err
	}
	m.backendMap[name] = preBackend
	flowLog.Debugf("backend->[%s] add finished", preBackend)
	flowLog.Debugf("done")
	return nil
}

func (m *Manager) deleteBackend(name string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	if runningBackend, ok := m.backendMap[name]; ok {
		err := runningBackend.Close()
		if err != nil {
			flowLog.Errorf("close backend->[%s] failed,error:%s", name, err)
			return err
		}
		delete(m.backendMap, name)
		flowLog.Debugf("backend->[%s] delete finished", runningBackend)
	}
	flowLog.Debugf("done")
	return nil
}

func (m *Manager) modifyBackend(name string, hostInfo *Info) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	var err error
	backend, ok := m.backendMap[name]
	if !ok {
		flowLog.Errorf("backend->[%s] not found when try to modify it", name)
		return ErrBackendNotExist
	}

	backendConfig, err := m.makeBackendConfig(name, hostInfo)
	if err != nil {
		flowLog.Errorf("init backendConfig failed")
		return err
	}

	// 目前reset只是重置了内部的一些参数，这样做是否可行还待验证
	err = backend.Reset(backendConfig)
	if err != nil {
		flowLog.Errorf("backend->[%s] reset failed,error:%s", name, err)
		return err
	}
	flowLog.Debugf("backend->[%s] modify finished", backend)
	flowLog.Debugf("done")
	return nil
}

func (m *Manager) makeBackendConfig(name string, hostInfo *Info) (*BasicConfig, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	if hostInfo == nil {
		return nil, fmt.Errorf("host info is empty")
	}
	cfg := MakeBasicConfig(name, hostInfo, viper.GetBool(common.ConfigKeyBackendForceBackup), viper.GetBool(common.ConfigKeyBackendIgnoreKafka), viper.GetDuration(common.ConfigKeyBackendTimeout))
	flowLog.Debugf("done")
	return cfg, nil
}

func (m *Manager) makeBackend(name string, hostInfo *Info) (Backend, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	// 初始化Backend
	backendConfig, err := m.makeBackendConfig(name, hostInfo)
	if err != nil {
		flowLog.Errorf("init backendConfig failed")
		return nil, err
	}
	backendFunc := GetBackendFunc("influxdb")
	preBackend, _, err := backendFunc(m.ctx, backendConfig)
	if err != nil {
		flowLog.Errorf("failed to init backend->[%s] for->[%s]", name, err)
		return nil, err
	}
	flowLog.Debugf("done")
	return preBackend, nil
}

// Backup 备份配置
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

	m.backupBackendMap = make(map[string]Backend)
	m.backupHostInfo = make(map[string]*Info)
	for k, v := range m.backendMap {
		m.backupBackendMap[k] = v
	}
	for k, v := range m.usingHostInfo {
		m.backupHostInfo[k] = v
	}
}

// Recover 恢复配置
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
	if m.backupBackendMap == nil || m.backupHostInfo == nil {
		return ErrBackupIsNil
	}

	// 如果有添加，需要把添加的 backend 关闭，防止再次 reload 时，多次初始化
	for key, _ := range m.backendMap {
		if b, ok := m.backupBackendMap[key]; !ok {
			b.Close()
		}
	}

	m.backendMap = m.backupBackendMap
	m.usingHostInfo = m.backupHostInfo
	m.backupBackendMap = nil
	m.backupHostInfo = nil
	return nil
}
