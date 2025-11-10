// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package define

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
)

func ToHostPath(path string) string {
	return strings.ReplaceAll(path, config.HostPath, config.ContainerHostPath)
}

func GetContainerMount(path string, container *Container) (map[string]string, error) {
	mountMap := make(map[string]string)
	if !filepath.IsAbs(path) {
		err := fmt.Errorf("log path specified as \"%s\" is not an absolute path", path)
		return mountMap, err
	}

	// if target path mount host
	for _, mountInfo := range container.Mounts {
		if strings.HasPrefix(filepath.Join(path, string(filepath.Separator)), filepath.Join(mountInfo.ContainerPath, string(filepath.Separator))) {
			// 将挂载信息存入 mountMap
			mountMap[mountInfo.HostPath] = mountInfo.ContainerPath
		}
	}
	if len(mountMap) > 0 {
		return mountMap, nil
	}
	err := fmt.Errorf("ONLY support mounted container log path in windows")
	return mountMap, err
}
