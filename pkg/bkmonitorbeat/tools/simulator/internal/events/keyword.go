// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package events

import (
	"log"
	"os"
	"path"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

func MakeKeyword() {
	log.Println(time.Now(), "make keyword")
	dirPath := os.Getenv("TEST_PATH")
	raise := os.Getenv("RAISE")
	w := zapcore.AddSync(&lumberjack.Logger{
		Filename:   path.Join(dirPath, "keyword.log"),
		MaxSize:    1, // megabytes
		MaxBackups: 3,
		MaxAge:     1, // days
	})
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(zap.NewProductionEncoderConfig()),
		w,
		zap.InfoLevel,
	)
	logger := zap.New(core)
	if raise == "1" {
		logger.Error("test_name_hello: " + time.Now().String())
	} else {
		logger.Info("hello: " + time.Now().String())
	}
}
