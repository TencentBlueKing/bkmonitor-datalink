// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package rpmpackage

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

func RpmList(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "rpm", "-qa")

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	return strings.Split(stdout.String(), "\n"), nil
}

func RpmVerify(ctx context.Context, pkg string) (string, error) {
	cmd := exec.CommandContext(ctx, "rpm", "--verify", pkg)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}

	return stdout.String(), nil
}
