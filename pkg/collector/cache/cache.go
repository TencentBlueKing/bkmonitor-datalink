// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cache

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/cache/k8scache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Entity struct {
	Name   string         `config:"name" mapstructure:"name"`
	Config map[string]any `config:"config" mapstructure:"config"`
}

type Config []Entity

func Install(conf *confengine.Config) error {
	c := &Config{}
	if err := conf.UnpackChild(define.ConfigFieldCache, c); err != nil {
		return err
	}

	for _, entity := range *c {
		switch entity.Name {
		case k8scache.Name:
			cc := &k8scache.Config{}
			if err := mapstructure.Decode(entity.Config, cc); err != nil {
				return err
			}
			cc.Validate()
			if err := k8scache.Install(cc); err != nil {
				return err
			}
			logger.Infof("cache %s installed", entity.Name)
		}
	}

	return nil
}

func Uninstall() {
	k8scache.Uninstall() // noop if not installed
}
