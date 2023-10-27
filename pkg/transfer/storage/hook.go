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
	"os"
	"path/filepath"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	ConfStorageType    = "storage.type"
	ConfStorageDataDir = "storage.path"
	ConfStoragePerm    = "storage.file_permission"
	ConfCcCacheSize    = "storage.cc_cache_size"
	ConfStopCcCache    = "storage.is_stop_cache" // 是否关闭cmdb缓存，默认为false
	ConfStorageTaget   = "storage.target_path"   // 缓存目标文件，若配置这个，则直接使用这个
)

func initConfiguration(c define.Configuration) {
	cwd, err := os.Getwd()
	utils.CheckError(err)
	c.SetDefault(ConfStorageType, "memory")
	c.SetDefault(ConfStorageDataDir, filepath.Join(cwd, "data/"))
	c.SetDefault(ConfStoragePerm, "0740")
	c.SetDefault(ConfCcCacheSize, 500)
	c.SetDefault(ConfStopCcCache, false)
	c.SetDefault(ConfStorageTaget, "")
}

func readConfiguration(c define.Configuration) {
	root := c.GetString(ConfStorageDataDir)
	_, err := os.Stat(root)
	if err == nil {
		return
	} else if !os.IsNotExist(err) {
		panic(err)
	}

	perm, err := utils.StringToFilePerm(c.GetString(ConfStoragePerm))
	utils.CheckError(err)
	utils.CheckError(os.MkdirAll(root, perm))
}

func init() {
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, initConfiguration))
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPostParse, readConfiguration))
}
