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
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// key: table_id
var tableMap map[string]*TableInfo

var tableLock *sync.RWMutex

// ReloadTableInfo
func ReloadTableInfo(infos map[string]*TableInfo) error {
	storageLock.Lock()
	defer storageLock.Unlock()

	tableMap = infos
	for tableID, info := range infos {
		log.Debugf(context.TODO(), "reload table id:%s info:%v success", tableID, info)
	}
	return nil
}
