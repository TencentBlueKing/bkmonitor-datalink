// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package static

import (
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Status 控制任务状态，负责对任务启动的执行逻辑进行控制
type Status struct {
	checkLock  *sync.RWMutex
	reportLock *sync.RWMutex

	lastCheckTime  int64
	lastReportTime int64

	// 检查周期
	checkPeriod time.Duration
	// 上报周期
	reportPeriod time.Duration

	FirstReport bool

	// 随机延后上报周期
	randomPendingDuration time.Duration
}

// NewStatus :
func NewStatus(checkPeriod, reportPeriod time.Duration) *Status {
	return &Status{
		checkLock:    new(sync.RWMutex),
		reportLock:   new(sync.RWMutex),
		checkPeriod:  checkPeriod,
		reportPeriod: reportPeriod,
		FirstReport:  true,
	}
}

// ShouldCheck :
func (s *Status) ShouldCheck() bool {
	s.checkLock.RLock()
	defer s.checkLock.RUnlock()
	// 第一次检查直接返回true
	if s.lastCheckTime == 0 {
		logger.Debug("first check")
		return true
	}
	// 之后周期性返回true
	if time.Now().Sub(time.Unix(s.lastCheckTime, 0)) > s.checkPeriod {
		logger.Debug("reached check time")
		return true
	}
	return false
}

// ShouldReport 周期上报判断
func (s *Status) ShouldReport() bool {
	s.reportLock.RLock()
	defer s.reportLock.RUnlock()
	// 前期上报模式，前两次上报逻辑特殊，做了时间随机漂移处理
	if s.FirstReport {
		// 第一次上报
		if s.randomPendingDuration == 0 {
			logger.Debugf("first report")
			return true
		}
		// 到达随机上报时间点，开始正式周期上报
		if time.Now().Sub(time.Unix(s.lastReportTime, 0)) > s.randomPendingDuration {
			logger.Debugf("reached random pending time,should report")
			return true
		}
		return false
	}
	// 当前时间超过上次上报周期，则反馈需要上报
	if time.Now().Sub(time.Unix(s.lastReportTime, 0)) > s.reportPeriod {
		logger.Debugf("reached report time")
		return true
	}

	return false
}

// UpdateCheckTime :
func (s *Status) UpdateCheckTime(timestamp int64) {
	s.checkLock.Lock()
	defer s.checkLock.Unlock()
	logger.Debugf("update check time from [%d] to [%d]", s.lastCheckTime, timestamp)
	s.lastCheckTime = timestamp
	// 计算下次检查的时间,仅做日志参考
	logger.Debugf("next check time is:%s", time.Unix(s.lastCheckTime, 0).Add(s.checkPeriod))

}

// UpdateReportTime :
func (s *Status) UpdateReportTime(timestamp int64) {
	s.reportLock.Lock()
	defer s.reportLock.Unlock()
	nextDuration := s.reportPeriod
	if s.FirstReport {
		// 上报完第一次数据后，随机sleep 范围为[1,3600]秒
		if s.randomPendingDuration == 0 {
			s.randomPendingDuration = GetRandomDuration()
			nextDuration = s.randomPendingDuration
			logger.Debugf("set random pending duration to [%s]", s.randomPendingDuration)
		} else {
			logger.Debugf("random pending end,start period report")
			s.FirstReport = false
		}
	}
	logger.Debugf("update last report time from [%d] to [%d]", s.lastReportTime, timestamp)
	s.lastReportTime = timestamp
	// 计算下次上报的时间,仅做日志参考
	logger.Debugf("next report time is:%s", time.Unix(s.lastReportTime, 0).Add(nextDuration))

}
