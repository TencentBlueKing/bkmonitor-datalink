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
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// PrintTagMap :
type PrintTagMap map[string][]backend.Backend

func (m PrintTagMap) String() string {
	var buf bytes.Buffer
	for tagKey, backends := range m {
		buf.WriteString("\n\t")
		buf.WriteString(tagKey)
		for _, backend := range backends {
			buf.WriteString(fmt.Sprintf("\n\t\t%s", backend))
		}
	}

	return buf.String()
}

// TagInfoManager 管理所属cluster的tag路由
type TagInfoManager struct {
	ctx            context.Context
	cancel         context.CancelFunc
	clusterName    string
	groupBatch     int
	lock           sync.RWMutex
	allBackendList []backend.Backend
	readMap        PrintTagMap
	writeMap       PrintTagMap
}

// NewTagInfoManager :
func NewTagInfoManager(ctx context.Context, clusterName string, groupBatch int, allBackendList []backend.Backend) *TagInfoManager {
	ctx, cancel := context.WithCancel(ctx)
	return &TagInfoManager{
		ctx:            ctx,
		cancel:         cancel,
		clusterName:    clusterName,
		allBackendList: allBackendList,
		groupBatch:     groupBatch,
	}
}

// Reset 重置一些参数
func (t *TagInfoManager) Reset(groupBatch int, allBackendList []backend.Backend) {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.allBackendList = allBackendList
	t.groupBatch = groupBatch
}

// Stop 停止当前manager的运作
func (t *TagInfoManager) Stop() {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.cancel()
}

// WatchChange 监听当前路径tag
func (t *TagInfoManager) WatchChange() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"cluster": t.clusterName,
	})
	outChan, err := consul.WatchTagChange(t.ctx, t.clusterName)
	if err != nil {
		return err
	}
	go func() {
		for {
			select {
			case <-outChan:
				// 接到消息就刷新，不做校验
				flowLog.Info("start refresh tag")
				err := t.Refresh()
				if err != nil {
					flowLog.Errorf("refresh tag failed,error:%s", err)
					break
				}
				flowLog.Info("refresh done")
			case <-t.ctx.Done():
				return
			}
		}
	}()
	return nil
}

func (t *TagInfoManager) String() string {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return fmt.Sprintf("\nread tags:%s\nwrite tags:%s", t.readMap, t.writeMap)
}

func (t *TagInfoManager) getDefaultKey() string {
	return "__default__/__default__/__default__==__default__"
}

func (t *TagInfoManager) getWriteBackendsByLocal(tagsKey string) []backend.Backend {
	t.lock.RLock()
	defer t.lock.RUnlock()
	// 根据key寻找对应路由
	if backends, ok := t.writeMap[tagsKey]; ok {
		return backends
	}
	// 找不到则使用默认的
	if backends, ok := t.writeMap[t.getDefaultKey()]; ok {
		return backends
	}
	return nil
}

func (t *TagInfoManager) getReadBackendsByLocal(tagsKey string) []backend.Backend {
	t.lock.RLock()
	defer t.lock.RUnlock()
	// 根据key寻找对应路由
	if backends, ok := t.readMap[tagsKey]; ok {
		return backends
	}
	// 找不到则使用默认的
	if backends, ok := t.readMap[t.getDefaultKey()]; ok {
		return backends
	}
	return nil
}

// GetWriteBackends 根据tagsKey获取backends
func (t *TagInfoManager) GetWriteBackends(tagsKey string) ([]backend.Backend, error) {
	// 1.先在本地查询存储信息
	backends := t.getWriteBackendsByLocal(tagsKey)
	if backends != nil {
		return backends, nil
	}
	// 2.本地查不到，刷新信息后再查一次
	err := t.Refresh()
	if err != nil {
		return nil, err
	}
	backends = t.getWriteBackendsByLocal(tagsKey)
	if len(backends) == 0 {
		return nil, ErrMatchBackendByTag
	}

	return backends, nil
}

// GetReadBackends 根据tagsKey获取backends
func (t *TagInfoManager) GetReadBackends(tagsKey string) ([]backend.Backend, error) {
	// 读取不参与刷新逻辑
	// 1.先在本地查询存储信息
	backends := t.getReadBackendsByLocal(tagsKey)
	if backends != nil {
		return backends, nil
	}
	// 2.本地查不到，刷新信息后再查一次
	err := t.Refresh()
	if err != nil {
		return nil, err
	}
	backends = t.getReadBackendsByLocal(tagsKey)
	if len(backends) == 0 {
		return nil, ErrMatchBackendByTag
	}
	return backends, nil
}

func (t *TagInfoManager) GetReadKeys(routePrefix string) []string {
	results := make([]string, 0)
	// 默认路由要加上
	results = append(results, t.getDefaultKey())
	t.lock.RLock()
	defer t.lock.RUnlock()
	// 遍历readmap，获取所有可读的keys
	for key := range t.readMap {
		// 只取该库表相关的路由
		if strings.HasPrefix(key, routePrefix) {
			results = append(results, key)
		}
	}
	return results
}

// Refresh 刷新路由
func (t *TagInfoManager) Refresh() error {
	infos, err := consul.GetTagsInfo(t.clusterName)
	if err != nil {
		return err
	}
	readMap := make(map[string][]backend.Backend)
	writeMap := make(map[string][]backend.Backend)
	for key, info := range infos {
		readList := make([]backend.Backend, 0)
		writeList := make([]backend.Backend, 0)
		// host_list读写皆可
		for _, host := range info.HostList {
			for _, backend := range t.allBackendList {
				if host == backend.Name() {
					readList = append(readList, backend)
					writeList = append(writeList, backend)
				}
			}
		}
		// unreadable_host_list可写不可读
		for _, host := range info.UnreadableHost {
			for _, backend := range t.allBackendList {
				if host == backend.Name() {
					writeList = append(writeList, backend)
				}
			}
		}
		readMap[key] = readList
		writeMap[key] = writeList
	}
	t.lock.Lock()
	defer t.lock.Unlock()
	t.readMap = readMap
	t.writeMap = writeMap
	return nil
}
