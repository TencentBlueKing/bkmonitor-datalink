// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package instance

import (
	"context"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
)

var (
	source = "/Users/renjinming/Desktop/cos/1"
	target = "test/2"
)

func cosInit() *Cos {
	viper.Set("logger.level", "debug")

	logger := log.NewLogger()
	return &Cos{
		Region:         "ap-guangzhou",
		Url:            "cos-internal.ap-guangzhou.tencentcos.cn",
		Bucket:         "bkop-1258344700",
		SecretID:       "",
		SecretKey:      "",
		PartSize:       10 * 1024 * 1024,
		MaxRetries:     3,
		ThreadPoolSize: 10,
		Timeout:        time.Second * 30,
		Log:            logger,
	}
}

func TestUpload(t *testing.T) {
	ctx := context.Background()
	err := cosInit().Upload(ctx, source, target)
	assert.Nil(t, err)
}

func TestDownload(t *testing.T) {
	ctx := context.Background()
	tmpPath, err := cosInit().Download(ctx, "cos_temp", target)
	assert.Nil(t, err)
	assert.Equal(t, tmpPath, "cos_temp/cos_test")
}
