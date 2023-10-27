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
	"os"
	"path"

	cp "github.com/otiai10/copy"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
)

var _ Instance = (*Cfs)(nil)

type Cfs struct {
	Log log.Logger
}

func (c *Cfs) successLockPath(ctx context.Context, targetPath string) string {
	return path.Join(targetPath, successLock)
}

// Exist 判断文件是否存在
func (c *Cfs) Exist(ctx context.Context, targetPath string) (bool, error) {
	_, err := os.Stat(c.successLockPath(ctx, targetPath))
	return err == nil, nil
}

// Upload cfs 直接拷贝
func (c *Cfs) Upload(ctx context.Context, sourcePath, targetPath string) error {
	err := cp.Copy(sourcePath, targetPath)
	if err != nil {
		return err
	}

	f, err := os.Create(c.successLockPath(ctx, targetPath))
	if err != nil {
		return err
	}
	return f.Close()
}

// Download 下载文件，因为 cfs 是直接挂在的所以直接返回即可
func (c *Cfs) Download(ctx context.Context, sourcePath, targetPath string) (string, error) {
	return targetPath, nil
}

// Delete 删除文件
func (c *Cfs) Delete(ctx context.Context, targetPath string) error {
	err := os.RemoveAll(targetPath)
	return err
}
