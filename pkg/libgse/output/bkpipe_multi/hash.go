// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkpipe_multi

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"github.com/elastic/beats/libbeat/common"
)

// HashRawConfig 获取配置hash值
func HashRawConfig(config common.ConfigNamespace) (string, error) {
	source := map[string]interface{}{
		"__name__": config.Name(),
	}
	err := config.Config().Unpack(source)
	if err != nil {
		return "", err
	}
	rawConfig, err := json.Marshal(source)
	if err != nil {
		return "", err
	}
	return Md5(string(rawConfig)), err
}

// Md5 获取字符md5
func Md5(str string) string {
	h := md5.New()
	h.Write([]byte(str))
	return hex.EncodeToString(h.Sum(nil))
}
