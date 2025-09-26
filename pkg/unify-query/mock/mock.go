// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

var mockInitOnce sync.Once

func Init() {
	mockInitOnce.Do(func() {
		dir, _ := os.Getwd()
		dir, _ = filepath.Abs(dir)
		name := `bkmonitor-datalink/pkg/unify-query`
		rootDir := strings.Split(dir, name)
		path := fmt.Sprintf("%s%s/unify-query.yaml", rootDir[0], name)
		config.CustomConfigFilePath = path
		config.InitConfig()
		log.InitTestLogger()
		metadata.InitMetadata()
		ctx := metadata.InitHashID(context.Background())
		mockHandler(ctx)
	})
}
