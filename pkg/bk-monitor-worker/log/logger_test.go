// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package log

import (
	"bufio"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func TestLogger(t *testing.T) {
	InitLogger()
	// 测试打印日志
	infoMsg := "this is a info test"
	logger.Info(infoMsg)

	logFile := config.LoggerStdoutPath
	// 查询日志
	file, err := os.Open(logFile)
	if err != nil {
		t.Errorf("log file not found")
	}
	defer file.Close()

	// 过滤数据
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		content := string(scanner.Bytes())
		assert.Contains(t, content, infoMsg)
	}

	if err := scanner.Err(); err != nil {
		t.Errorf("scan file failed, %v", err)
	}

	// 删除日志文件
	if err := os.Remove(logFile); err != nil {
		t.Errorf("delete log file failed")
	}
}
