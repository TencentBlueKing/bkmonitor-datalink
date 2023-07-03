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
	"fmt"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
)

const (
	CfsName     = "cfs"
	CosName     = "cos"
	successLock = "success.lock"
)

var (
	mapInstances    = make(map[string]Instance)
	mapInstanceLock = new(sync.RWMutex)
)

type Instance interface {
	Exist(ctx context.Context, targetPath string) (bool, error)
	Upload(ctx context.Context, sourcePath, targetPath string) error
	Download(ctx context.Context, sourcePath, targetDir string) (string, error)
	Delete(ctx context.Context, targetPath string) error
}

// RegisterInstances 注册实例
func RegisterInstances(log log.Logger) error {
	mapInstanceLock.Lock()
	defer mapInstanceLock.Unlock()

	// 加载配置
	err := ConfigInit()
	if err != nil {
		return err
	}

	// 注册 cfs
	mapInstances[CfsName] = &Cfs{
		Log: log,
	}

	// 注册 cos
	mapInstances[CosName] = &Cos{
		Log:            log,
		Region:         CosRegion,
		Url:            CosUrl,
		Bucket:         fmt.Sprintf("%s-%s", CosBucket, CosAppID),
		SecretID:       CosSecretID,
		SecretKey:      CosSecretKey,
		PartSize:       CosPartSize,
		MaxRetries:     CosMaxRetries,
		ThreadPoolSize: CosThreadPoolSize,
		Timeout:        CosTimeout,
		TempDir:        CosTempDir,
	}

	return nil
}

func GetInstance(k string) (Instance, error) {
	mapInstanceLock.RLock()
	defer mapInstanceLock.RUnlock()
	if ins, ok := mapInstances[k]; ok {
		return ins, nil
	} else {
		return nil, fmt.Errorf("instance %s is not exist", k)
	}
}
