// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package task

import (
	"context"
	"sync"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// RefreshDefaultRp  更新每个influxdb的存储RP
func RefreshDefaultRp(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("Runtime panic caught: %v\n", err)
		}
	}()

	db := mysql.GetDBSession().DB
	var influxdbHostList []storage.InfluxdbHostInfo
	if err := storage.NewInfluxdbHostInfoQuerySet(db).All(&influxdbHostList); err != nil {
		return errors.Wrap(err, "query InfluxdbHostInfo failed")
	}

	wg := sync.WaitGroup{}
	ch := make(chan bool, GetGoroutineLimit("refresh_default_rp"))
	wg.Add(len(influxdbHostList))
	for _, host := range influxdbHostList {
		ch <- true
		go func(host storage.InfluxdbHostInfo, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			var svc = service.NewInfluxdbHostInfoSvc(&host)
			err := svc.RefreshDefaultRp()
			if err != nil {
				logger.Errorf("refresh default rp for [%s] failed, %v", host.HostName, err)
				return
			}
			logger.Infof("refresh default rp for [%s] success", host.HostName)
		}(host, &wg, ch)
	}
	wg.Wait()

	return nil
}
