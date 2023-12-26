// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mocker

import (
	"fmt"
	"time"

	"github.com/IBM/sarama"
	"github.com/agiledragon/gomonkey/v2"
	client "github.com/influxdata/influxdb1-client/v2"
	"github.com/jinzhu/gorm"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
)

func InitTestDBConfig(filePath string) {
	config.FilePath = filePath
	config.InitConfig()
	gomonkey.ApplyFunc(mysql.GetDBSession, func() *mysql.DBSession {
		db, err := gorm.Open("mysql", fmt.Sprintf(
			"%s:%s@tcp(%s:%v)/%s?&parseTime=True&loc=Local",
			config.StorageMysqlUser,
			config.StorageMysqlPassword,
			config.StorageMysqlHost,
			config.StorageMysqlPort,
			config.StorageMysqlDbName,
		))
		if err != nil {
			panic(err)
		}
		return &mysql.DBSession{DB: db}
	})
}

type KafkaClientMocker struct {
	sarama.Client
	PartitionMap map[string][]int32
}

func (k *KafkaClientMocker) Partitions(topic string) ([]int32, error) {
	return k.PartitionMap[topic], nil
}

func (k *KafkaClientMocker) Close() error { return nil }

type InfluxDBClientMocker struct {
	client.Client
}

func (i *InfluxDBClientMocker) Ping(timeout time.Duration) (time.Duration, string, error) {
	return 0, "", nil
}

func (i *InfluxDBClientMocker) Close() error { return nil }
