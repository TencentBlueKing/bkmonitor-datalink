// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cipher

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
)

// TransformDataidToToken 将dataid 加密为bk.data.token, 不存在的id填-1
func TransformDataidToToken(metricDataId, traceDataId, logDataId, bkBizId int, appName string) string {
	// bk.data.token=${metric_data_id}${salt}${trace_data_id}${salt}${log_data_id}${salt}${bk_biz_id}
	bkDataTokenRaw := fmt.Sprintf(
		"%v%s%v%s%v%s%v%s%s",
		metricDataId, config.BkdataTokenSalt,
		traceDataId, config.BkdataTokenSalt,
		logDataId, config.BkdataTokenSalt,
		bkBizId, config.BkdataTokenSalt,
		appName,
	)
	var xKey string
	if config.BkdataAESKey != "" {
		xKey = config.BkdataAESKey
	} else {
		xKey = config.AesKey
	}
	iv := []byte(config.BkdataAESIv)
	c := NewAESCipher(xKey, "", iv)
	token := c.AESEncrypt(bkDataTokenRaw)
	return token
}
