// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package toolkit

import (
	"context"
	"os/exec"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const defaultNetStatTimeout = time.Second * 5

func ListRouteTable() (string, error) {
	ctx, _ := context.WithTimeout(context.Background(), defaultNetStatTimeout)
	bytes, err := exec.CommandContext(ctx, "netstat", "-rn").CombinedOutput()
	if err != nil {
		logger.Errorf("exec netstat -rn failed: %v", err)
		return "", err
	}
	return string(bytes), nil
}
