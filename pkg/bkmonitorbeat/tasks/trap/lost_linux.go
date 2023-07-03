// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trap

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func (g *Gather) watchUdpLost(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Warn("watch udp lost exit")
			return
		case <-ticker.C:
			logger.Debug("check udp lost start")
			g.checkLostUdpCount(ctx)
		}
	}
}

// 检查是否有udp包丢失
func (g *Gather) checkLostUdpCount(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	data, err := os.ReadFile("/proc/net/snmp")
	if err != nil {
		logger.Errorf("read file failed,error:%s", err)
		return
	}

	lines := strings.Split(string(data), "\n")
	count := 0
	errIndex := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "Udp:") {
			count++
			// 获取 RcvbufErrors的定位
			if count == 1 {
				fields := strings.Fields(line)
				for index, field := range fields {
					if field == "RcvbufErrors" {
						errIndex = index
					}
				}
			}
			// 取值
			if count == 2 {
				fields := strings.Fields(line)
				if errIndex != 0 && len(fields) > errIndex {
					lostCount, err := strconv.ParseInt(fields[errIndex], 0, 64)
					if err != nil {
						logger.Errorf("parse receive buffer error number failed,error:%s", err)
						return
					}
					if lostCount != g.udpLostCount {
						logger.Errorf("got increasing number of udp lost report,last num:%d,current num:%d", g.udpLostCount, lostCount)
					}
					g.udpLostCount = lostCount
				}
			}
		}
	}
}
