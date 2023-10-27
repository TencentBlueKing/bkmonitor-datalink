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
	"encoding/json"
	"sync"
)

// BalanceMap :
type BalanceMap struct {
	balance map[string]int64
	lock    *sync.RWMutex
	maxSize int64
}

// NewBalanceMap :
func NewBalanceMap(maxSize int) *BalanceMap {
	return &BalanceMap{
		balance: make(map[string]int64),
		lock:    new(sync.RWMutex),
		maxSize: 5000,
	}
}

func (m *BalanceMap) String() string {
	m.lock.RLock()
	defer m.lock.RUnlock()
	data, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(data)
}

// GetCount 获取对应tag的均衡计数
func (m *BalanceMap) GetCount(name string) int64 {
	m.lock.Lock()
	defer m.lock.Unlock()
	count, ok := m.balance[name]
	if !ok {
		count = 0
	}
	m.balance[name] = count + 1
	return count
}

type Series struct {
	Name    string     `json:"name"`
	Columns []string   `json:"columns"`
	Values  [][]string `json:"values"`
}
type Result struct {
	StatementID int       `json:"statement_id"`
	Series      []*Series `json:"series"`
}
type Info struct {
	Results []*Result `json:"results"`
}
