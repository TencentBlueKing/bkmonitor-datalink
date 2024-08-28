// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package operator

import (
	"fmt"
	"reflect"

	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/config"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/compressor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func (c *Operator) GetPromScrapeConfigs() ([]config.ScrapeConfig, bool) {
	var configs []config.ScrapeConfig
	round := make(map[string][]byte) // 本轮获取到的数据
	for _, conf := range ConfPromSdConfigs {
		m, err := c.getPromSdConfigs(conf)
		if err != nil {
			logger.Errorf("get secrets sesource failed, config=(%v), err: %v", conf, err)
			continue
		}

		for k, v := range m {
			sdc, err := unmarshalPromSdConfigs(v)
			if err != nil {
				logger.Errorf("unmarshal prom sdconfigs failed, filename=(%s), err: %v", k, err)
				continue
			}

			round[k] = v
			configs = append(configs, sdc...)
		}
	}

	diff := reflect.DeepEqual(c.promSdConfigsBytes, round) // 对比是否需要更新操作
	c.promSdConfigsBytes = round
	return configs, diff
}

func (c *Operator) getPromSdConfigs(sdConfig PromSDConfig) (map[string][]byte, error) {
	// 需要同时指定 namespace/name
	if sdConfig.Namespace == "" || sdConfig.Name == "" {
		return nil, errors.New("empty sdconfig namespace/name")
	}
	secretClient := c.client.CoreV1().Secrets(sdConfig.Namespace)
	secret, err := secretClient.Get(c.ctx, sdConfig.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	uncompressed := make(map[string][]byte)
	for file, data := range secret.Data {
		d, err := compressor.Uncompress(data)
		if err != nil {
			return nil, err
		}
		uncompressed[sdConfigsKeyFunc(sdConfig, file)] = d
	}

	return uncompressed, nil
}

func sdConfigsKeyFunc(sdConfig PromSDConfig, file string) string {
	return fmt.Sprintf("%s/%s/%s", sdConfig.Namespace, sdConfig.Name, file)
}

func unmarshalPromSdConfigs(b []byte) ([]config.ScrapeConfig, error) {
	var objs []interface{}
	if err := yaml.Unmarshal(b, &objs); err != nil {
		return nil, err
	}

	var ret []config.ScrapeConfig
	for i := 0; i < len(objs); i++ {
		obj := objs[i]
		var sc config.ScrapeConfig

		bs, err := yaml.Marshal(obj)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(bs, &sc); err != nil {
			return nil, err
		}
		ret = append(ret, sc)
	}

	return ret, nil
}
