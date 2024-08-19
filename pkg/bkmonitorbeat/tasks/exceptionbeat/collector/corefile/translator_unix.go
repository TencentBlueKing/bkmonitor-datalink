// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package corefile

import (
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type Translator interface {
	Translate(text string) string
}

type SignalTranslator struct{}

func (t *SignalTranslator) Translate(text string) string {
	// 将对应的信号值，转化为信号名
	signalNum, err := strconv.Atoi(text)
	if err != nil {
		return text
	}
	return unix.SignalName(syscall.Signal(signalNum))
}

type ExecutablePathTranslator struct{}

func (t *ExecutablePathTranslator) Translate(text string) string {
	// 将路径中的"!"替换为"/"
	return strings.ReplaceAll(text, "!", "/")
}
