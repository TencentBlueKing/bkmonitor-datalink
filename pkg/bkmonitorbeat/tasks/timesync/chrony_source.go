// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package timesync

import (
	"time"

	"github.com/facebook/time/ntp/chrony"
)

// chronySourceDetail 为单条 chrony source 生成新增指标的按地址明细。
//
// 只处理 NTP server/peer（SourceModeClient / SourceModePeer）；参考时钟与本地时钟
// （SourceModeRef）不是 NTP 地址，不生成明细。
//
// chrony 回包没有时间源自己的发送时间戳（SourceData 只有 LatestMeas、SinceSample 等字段），
// 因此成功条目统一使用本次采集时刻作为明细时间戳；不可达条目只上报可用性，不携带源时间。
func chronySourceDetail(sourceData *chrony.SourceData, now time.Time) (SourceResult, bool) {
	if sourceData.Mode != chrony.SourceModeClient && sourceData.Mode != chrony.SourceModePeer {
		return SourceResult{}, false
	}

	detail := SourceResult{
		Address:    sourceData.IPAddr.String(),
		Target:     "chrony",
		Available:  sourceData.State != chrony.SourceStateUnreach,
		SourceTime: now,
	}
	if !detail.Available {
		detail.SourceTime = time.Time{}
		return detail, true
	}

	detail.OffsetSeconds = sourceData.LatestMeas
	return detail, true
}
