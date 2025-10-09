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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func init() {
	storageMap = make(map[string]Client)
	storageLock = new(sync.RWMutex)

	tableMap = make(map[string]*TableInfo)
	tableLock = new(sync.RWMutex)

	aliasMap = make(map[string]map[string]bool)
	aliasLock = new(sync.RWMutex)
}

// SearchByStorage 根据存储信息以及查询语句，获取es查询结果
func SearchByStorage(storageID int, body string, aliases []string) (string, error) {
	storageLock.RLock()
	defer storageLock.RUnlock()
	client, ok := storageMap[strconv.Itoa(storageID)]
	if !ok {
		return "", metadata.Sprintf(
			metadata.MsgQueryES,
			"查询失败",
		).Error(context.TODO(), ErrStorageNotFound)
	}
	return client.Search(body, aliases...)
}

// GetStorageID 根据 tableID 获取 es 集群信息
func GetStorageID(tableID string) (*TableInfo, error) {
	tableLock.RLock()
	defer tableLock.RUnlock()
	if tableInfo, ok := tableMap[tableID]; ok {
		return tableInfo, nil
	}
	return nil, ErrStorageIDNotFound
}
