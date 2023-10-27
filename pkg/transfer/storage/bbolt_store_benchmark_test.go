// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build bbolt
// +build bbolt

package storage_test

import (
	"os"
	"path/filepath"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/storage"
)

//go:generate genny -in store_benchmark_template_test.tpl -pkg ${GOPACKAGE} -out bbolt_store_benchmark_gen_test.go gen T=BBolt

func newBBolt() define.Store {
	dir, err := os.MkdirTemp("", "bbolt")
	if err != nil {
		panic(err)
	}

	store, err := storage.NewBBoltStore("test", filepath.Join(dir, "transfer.db"), 0o666, nil)
	if err != nil {
		panic(err)
	}
	return store
}
