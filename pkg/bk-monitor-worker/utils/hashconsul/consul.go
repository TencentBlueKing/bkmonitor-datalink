// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package hashconsul

import (
	"fmt"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func Put(c *consul.Instance, key, val string) error {
	// 将中文转化为unicode
	var unicodeVal string
	for _, runeValue := range val {
		if utf8.ValidRune(runeValue) && runeValue >= 128 {
			unicodeVal += fmt.Sprintf("\\u%04X", runeValue)
		} else {
			unicodeVal += string(runeValue)
		}
	}
	val = unicodeVal

	oldValueBytes, err := c.Get(key)
	if err != nil {
		logger.Infof("can not get old value from [%s] because of [%v], will refresh consul", key, err)
		return c.Put(key, val, 0)
	}
	oldValue := string(oldValueBytes)
	equal, err := jsonx.CompareJson(oldValue, val)
	if err != nil {
		logger.Infof("can not compare new value [%s] and old value [%s], will refresh consul", key, err)
		return c.Put(key, val, 0)
	}
	if !equal {
		logger.Infof("new value [%s] is different from [%s] on consul, will updated it", val, oldValue)
		return c.Put(key, val, 0)
	}
	logger.Infof("new value [%s] is same with [%s] on consul, skip it", val, oldValue)
	return nil
}
