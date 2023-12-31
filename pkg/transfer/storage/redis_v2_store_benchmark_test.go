// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build redis_v2
// +build redis_v2

package storage_test

import (
	"github.com/alicebob/miniredis/v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/storage"
)

//go:generate genny -in store_benchmark_template_test.tpl -pkg ${GOPACKAGE} -out redis_v2_store_benchmark_gen_test.go gen T=Reids

func newReids() define.Store {
	s, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	store, _ := storage.NewRedisStore("standalone", "mymaster", s.Addr(), "",
		"bkmonitorv3.transfer.cmdb.cache", "", nil, 11, 10,
		10, 10, nil, nil, "", 0, false)
	return store
}
