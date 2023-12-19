// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestKafkaTopicInfoSvc_CreateInfo(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	var bkDataId uint = 11223344
	mysql.GetDBSession().DB.Delete(storage.KafkaTopicInfo{}, "bk_data_id = ?", bkDataId)
	topicInfo, err := NewKafkaTopicInfoSvc(nil).CreateInfo(bkDataId, "", 0, nil, nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("0bkmonitor_%v0", bkDataId), topicInfo.Topic)
	assert.Equal(t, 1, topicInfo.Partition)
	// exist error
	_, err = KafkaTopicInfoSvc{nil}.CreateInfo(bkDataId, "", 2, nil, nil, nil)
	assert.Error(t, err)

}
