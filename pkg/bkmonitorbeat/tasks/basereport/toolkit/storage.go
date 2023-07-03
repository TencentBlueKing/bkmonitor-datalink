// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package toolkit

import (
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	LastPublishTime = "last_publish_time"
)

// 将最后一次上报时间写入到持久化存储中
func RecordTaskPublishTime(taskname, time string) error {
	err := storage.Set(taskname, time, 0)
	if err != nil {
		logger.Errorf("set %s failed :%s", taskname, err)
		return err
	}
	return nil
}

// 将时间从持久化存储中读取出来并格式化
func ReadTimeFromDB(timekey string) (int64, error) {
	val, err := storage.Get(timekey)
	if err != nil {
		logger.Errorf("read %s failed :%s", timekey, err)
		return 0, err
	}
	logger.Debugf("read %s:%s", timekey, val)
	lastTime, err := strconv.Atoi(val)
	if err != nil {
		logger.Errorf("parse %s error :%s", timekey, err)
		return 0, err
	}
	return int64(lastTime), nil
}

// 检测时间是否相隔 1min（去除秒）,time1,2不分前后
func IsSameMinTime(time1, time2 int64) bool {
	time1second := time1 % 60
	time2second := time2 % 60

	return (time2 - time2second) == (time1 - time1second)
}

// true为相差大于等于1min，false为相差小于1min
func IsDiffMinTimeWithTask(taskname string, nowtime time.Time, period time.Duration) bool {
	if period != time.Minute {
		// 目前值适配周期为1min上报，非1min 直接返回true
		return true
	}
	lasttime, err := ReadTimeFromDB(taskname)
	if err != nil {
		logger.Info("read %s err: %s", taskname, err)
		// 读取出错则默认为为之前未上报，即相差1min以上
		return true
	}
	return !IsSameMinTime(lasttime, nowtime.Unix())
}

func IsDiffMinLastPublish(nowtime time.Time, period time.Duration) bool {
	return IsDiffMinTimeWithTask(LastPublishTime, nowtime, period)
}

func RecordPublishTime(time int64) error {
	return RecordTaskPublishTime(LastPublishTime, strconv.FormatInt(time, 10))
}
