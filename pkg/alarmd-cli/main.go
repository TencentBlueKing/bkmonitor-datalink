// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"os"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd-cli/internal/cli"
)

var version = "dev"

func main() {
	os.Exit(cli.New(os.Stdin, os.Stdout, os.Stderr, version).Run(os.Args[1:]))
}
