// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store

import (
	"time"
)

type Store interface {
	Open() error                                                             // 创建一个连接
	Put(key, val string, modifyIndex uint64, expiration time.Duration) error // 写入数据
	Get(key string) (uint64, []byte, error)                                  // 通过 key 获取数据
	Delete(key string) error                                                 // 删除 key
	Close() error                                                            // 关闭连接
}

type DummyStore struct{}

func (*DummyStore) Open() error { return nil }

func (*DummyStore) Put(key, val string, modifyIndex uint64, expiration time.Duration) error {
	return nil
}

func (*DummyStore) Get(key string) (uint64, []byte, error) { return uint64(0), nil, nil }

func (*DummyStore) Delete(key string) error { return nil }

func (*DummyStore) Close() error { return nil }

func CreateDummyStore() Store { return &DummyStore{} }
