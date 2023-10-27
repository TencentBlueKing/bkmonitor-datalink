// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"time"
)

type CacheItem struct {
	key     string
	data    []byte
	expires time.Duration
}

// 缓存通道
var CacheChan chan CacheItem

// 更新信号
var UpdateSignal chan struct{}

type StoreCache struct {
	data    []byte
	expires time.Duration
}

var WaitCache = func() {}

const (
	OnlyLeader = iota
	AllService
)

var StoreMode = map[string]int{
	// default: AllService
	"bbolt": AllService,
	"redis": OnlyLeader,
}
