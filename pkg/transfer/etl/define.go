// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

var (
	ErrTypeNotSupported = errors.Wrap(define.ErrType, "type not supported")
	ErrFieldNotReady    = errors.New("field not ready")
)

// Container :
type Container interface {
	Keys() []string
	Del(string) error
	Get(string) (interface{}, error)
	Put(string, interface{}) error
	Copy() Container
}

// Transformer
type Transformer interface {
	define.Stringer
	Transform(from Container, to Container) error
}

// Field : 处理单个字段
type Field interface {
	Transformer
	Name() string
	DefaultValue() (interface{}, bool)
}

// Record : 处理单个层级
type Record interface {
	Transformer
	Name() string
	Finish() error
}

// Schema
type Schema interface {
	Transformer
}

// FissionFn
type FissionFn func(from Container, callback func(fission Container) error) error

// TransformFn :
type TransformFn func(from interface{}) (to interface{}, err error)

type CheckFn func(v interface{}) bool

// ExtractFn :
type ExtractFn func(container Container) (value interface{}, err error)
