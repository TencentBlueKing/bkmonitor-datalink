// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"bytes"

	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
)

// ViperConfiguration :
type ViperConfiguration struct {
	*viper.Viper
}

// Sub :
func (c *ViperConfiguration) Sub(key string) Configuration {
	return NewViperConfiguration(c.Viper.Sub(key))
}

// Marshal :
func (c *ViperConfiguration) Marshal(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	c.SetConfigType("json")
	return c.ReadConfig(bytes.NewBuffer(data))
}

// UnmarshalKey :
func (c *ViperConfiguration) UnmarshalKey(key string, rawVal interface{}, opts ...interface{}) error {
	v := c.Viper.Sub(key)
	return c.decodeViper(v, rawVal, opts...)
}

// Unmarshal :
func (c *ViperConfiguration) Unmarshal(rawVal interface{}, opts ...interface{}) error {
	return c.decodeViper(c.Viper, rawVal, opts...)
}

func (c *ViperConfiguration) decodeViper(v *viper.Viper, rawVal interface{}, opts ...interface{}) error {
	if v == nil {
		return nil
	}

	conf := &mapstructure.DecoderConfig{
		Metadata:         nil,
		Result:           rawVal,
		WeaklyTypedInput: true,
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		),
	}

	for _, o := range opts {
		opt, ok := o.(viper.DecoderConfigOption)
		if !ok {
			return errors.Wrapf(ErrType, "unsupported option type %T", o)
		}
		opt(conf)
	}

	decoder, err := mapstructure.NewDecoder(conf)
	if err != nil {
		return err
	}

	return decoder.Decode(v.AllSettings())
}

// NewViperConfiguration : create a Configuration from viper
func NewViperConfiguration(v *viper.Viper) Configuration {
	return &ViperConfiguration{
		Viper: v,
	}
}
