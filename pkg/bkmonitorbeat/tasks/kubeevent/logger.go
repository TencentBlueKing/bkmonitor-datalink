// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kubeevent

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"

type wrapperLogger struct{}

func (wrapperLogger) Fatal(v ...interface{}) { logger.Fatal(v...) }

func (wrapperLogger) Fatalf(format string, v ...interface{}) { logger.Fatalf(format, v...) }

func (wrapperLogger) Fatalln(v ...interface{}) { logger.Fatal(v...) }

func (wrapperLogger) Panic(v ...interface{}) { logger.Panic(v...) }

func (wrapperLogger) Panicf(format string, v ...interface{}) { logger.Panicf(format, v...) }

func (wrapperLogger) Panicln(v ...interface{}) { logger.Panic(v...) }

func (wrapperLogger) Print(v ...interface{}) { logger.Info(v...) }

func (wrapperLogger) Printf(format string, v ...interface{}) { logger.Infof(format, v...) }

func (wrapperLogger) Println(v ...interface{}) { logger.Info(v...) }
