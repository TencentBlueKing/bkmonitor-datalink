// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package common

import (
	"io"
	"time"
)

// Configuration ...
type Configuration interface {
	ConfigFileUsed() string
	Get(key string) interface{}
	GetString(key string) string
	GetBool(key string) bool
	GetInt(key string) int
	GetInt32(key string) int32
	GetInt64(key string) int64
	GetFloat64(key string) float64
	GetTime(key string) time.Time
	GetDuration(key string) time.Duration
	GetStringSlice(key string) []string
	GetStringMap(key string) map[string]interface{}
	GetStringMapString(key string) map[string]string
	GetStringMapStringSlice(key string) map[string][]string
	GetSizeInBytes(key string) uint
	Marshal(v interface{}) error
	UnmarshalKey(key string, rawVal interface{}, opts ...interface{}) error
	Unmarshal(rawVal interface{}, opts ...interface{}) error
	// UnmarshalExact unmarshals the config into a Struct, erroring if a field is nonexistent
	// in the destination struct.
	UnmarshalExact(rawVal interface{}) error
	IsSet(key string) bool
	RegisterAlias(alias string, key string)
	InConfig(key string) bool
	SetDefault(key string, value interface{})
	Set(key string, value interface{})
	AllKeys() []string
	AllSettings() map[string]interface{}
	Sub(key string) Configuration
	ReadConfig(in io.Reader) error
	SetConfigType(string)
	SetEnvPrefix(in string)
	AutomaticEnv()
	SetConfigFile(in string)
	ReadInConfig() error
	// added by tsdbproxy, to explicit reload the config file.
	ReloadConfigFile() error
}
