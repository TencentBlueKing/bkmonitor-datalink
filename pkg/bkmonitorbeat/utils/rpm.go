// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/disk"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var innerMajor, innerMinor int64

func GetRootDevices() (int64, int64, error) {
	if innerMajor == 0 && innerMinor == 0 {
		major, minor, err := rootDevice()
		if err != nil {
			return 0, 0, err
		}
		innerMajor = int64(major)
		innerMinor = int64(minor)
	}

	return innerMajor, innerMinor, nil
}

func rootDevice() (uint64, uint64, error) {
	partitions, err := disk.Partitions(false)
	if err != nil {
		return 0, 0, err
	}

	var root string
	for _, partition := range partitions {
		if partition.Mountpoint == "/" {
			root = partition.Device
			break
		}
	}

	if root == "" {
		return 0, 0, err
	}

	stats, err := disk.IOCounters(root)
	if err != nil {
		return 0, 0, err
	}

	for _, stat := range stats {
		return stat.Major, 0, nil // 0 minor 为 disk，可通过 lsblk 查看
	}
	return 0, 0, errors.New("no root device found")
}

func rpmList(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "rpm", "-qa")

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	return strings.Split(stdout.String(), "\n"), nil
}

func rpmVerify(ctx context.Context, pkg string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "rpm", "--verify", pkg)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

type RpmResult struct {
	Package string `json:"package"`
	Verify  string `json:"verify"`
}

func RpmVerify(ctx context.Context) ([]RpmResult, error) {
	pkgs, err := rpmList(ctx)
	if err != nil {
		return nil, err
	}

	var ret []RpmResult
	for _, pkg := range pkgs {
		if pkg == "" {
			continue
		}
		time.Sleep(time.Millisecond * 100)
		stdout, stderr, err := rpmVerify(ctx, pkg)
		if err != nil {
			logger.Warnf("failed to verfiy rpm package %s: %v", pkg, err)
		}

		select {
		case <-ctx.Done():
			return ret, nil
		default:
		}

		switch {
		case stdout != "":
			ret = append(ret, RpmResult{
				Package: pkg,
				Verify:  stdout,
			})
		case stderr != "":
			ret = append(ret, RpmResult{
				Package: pkg,
				Verify:  stderr,
			})
		}
	}

	return ret, nil
}
