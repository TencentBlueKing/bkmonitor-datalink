// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package stores

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
)

func TestShards(t *testing.T) {

	sourceDir := "/shard-old"
	targeName := "cfs"
	targetDir := "/shard-new"
	database := "db"
	clusterName := "default"
	instanceName := "influxdb"
	address := ""
	tagRouter := "k=v"

	logger := log.NewLogger()

	influxDB := NewInfluxDB(
		logger, clusterName, instanceName, tagRouter,
		sourceDir, targeName, targetDir, address, "", "",
	)
	shards := influxDB.GetActiveShards(context.TODO(), database, nil)
	expected := `{"defaultdbautogenkv16725312001673049600":{"meta":{"cluster_name":"default","database":"db","retention_policy":"autogen","tag_name":"k","tag_value":"v"},"spec":{"start":"2023-01-01T00:00:00Z","end":"2023-01-07T00:00:00Z","source":{"instance_type":"influxdb","name":"influxdb","shard_id":2,"path":"/shard-old/data/db/autogen/2"},"target":{"instance_type":"cfs","name":"cfs","shard_id":2,"path":"/shard-new/influxdb/db/autogen/2"}},"status":{"code":1,"message":""}}}`

	actual, err := json.Marshal(shards)

	assert.Nil(t, err)
	assert.Equal(t, expected, string(actual))
}

func TestGetShards(t *testing.T) {
	sourceDir := "/shard-old"
	targeName := "cfs"
	targetDir := "/shard-new"
	clusterName := "default"
	instanceName := "influxdb"
	database := "test_api"
	address := ""
	tagRouter := "k=v"

	ctx := context.Background()
	logger := log.NewLogger()

	influxDB := NewInfluxDB(
		logger, clusterName, instanceName, tagRouter,
		sourceDir, targeName, targetDir, address, "", "",
	)

	simpleShards, err := influxDB.GetLocalShards(ctx, database)

	assert.Nil(t, err)
	assert.Equal(t, true, len(simpleShards) > 0)

}
