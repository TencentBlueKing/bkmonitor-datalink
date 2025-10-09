// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package es

import (
	"context"
	"strconv"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// 每个table_id对应一个index，也就对应一组alias
var aliasMap map[string]map[string]bool

var aliasLock *sync.RWMutex

// AliasExist 判断别名是否存在
func AliasExist(tableID string, alias string) bool {
	aliasLock.RLock()
	defer aliasLock.RUnlock()
	// 先捞tableid
	aliases, ok := aliasMap[tableID]
	if !ok {
		return false
	}
	// 再捞里面的alias
	_, ok = aliases[alias]
	return ok
}

// RefreshAllAlias 并发刷新整个别名map
func RefreshAllAlias() {
	// 根据table进行刷新，所以获取的是table的锁
	tableLock.RLock()
	defer tableLock.RUnlock()
	wg := new(sync.WaitGroup)
	for tableID, info := range tableMap {
		wg.Add(1)
		go func(tableID string, storageID int, indexName string) {
			defer wg.Done()
			err := refreshAlias(tableID, storageID, indexName)
			if err != nil {
				log.Errorf(
					context.TODO(),
					"refresh alias failed,table_id:%s,storage_id:%d,index_name:%s,err:%s",
					tableID, storageID, indexName, err.Error())
				return
			}
		}(tableID, info.StorageID, ConvertTableIDToFuzzyIndexName(tableID))

	}
	wg.Wait()

	aliasLock.RLock()
	defer aliasLock.RUnlock()
	log.Debugf(context.TODO(), "refresh alias success,alias:%v", aliasMap)
}

// refreshAlias 刷新 es index alias
func refreshAlias(tableID string, storageID int, indexName string) error {
	var aliasData map[string]*AliasInfo
	// 查询对应index的alias信息
	data, err := aliasWithIndex(storageID, indexName)
	if err != nil {
		return err
	}
	// 将信息按格式反序列化，并填充到map中
	err = json.Unmarshal([]byte(data), &aliasData)
	if err != nil {
		return err
	}
	result := make(map[string]bool)
	for _, info := range aliasData {
		if len(info.Aliases) != 0 {
			for aliasName := range info.Aliases {
				result[aliasName] = true
			}
		}
	}

	aliasLock.Lock()
	defer aliasLock.Unlock()
	aliasMap[tableID] = result
	return nil
}

// aliasWithIndex 通过 index 获取 alias
func aliasWithIndex(storageID int, index string) (string, error) {
	storageLock.RLock()
	defer storageLock.RUnlock()
	client, ok := storageMap[strconv.Itoa(storageID)]
	if !ok {
		return "", ErrStorageNotFound
	}
	return client.AliasWithIndex(index)
}
