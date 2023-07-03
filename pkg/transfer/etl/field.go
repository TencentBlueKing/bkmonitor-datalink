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
	"sync"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

// FieldDefaultValueCreator
type FieldDefaultValueCreator func() interface{}

// BaseField :
type BaseField struct {
	name            string
	defaultsCreator FieldDefaultValueCreator
	hasDefaultValue bool
}

// Name
func (f *BaseField) Name() string {
	return f.name
}

// String : get field name
func (f *BaseField) String() string {
	return f.name
}

// DefaultValue : get default value
func (f *BaseField) DefaultValue() (interface{}, bool) {
	if !f.hasDefaultValue {
		return nil, false
	}

	return f.defaultsCreator(), true
}

// NewBaseField :
func NewBaseField(name string, defaultValue interface{}, hasDefaultValue bool) *BaseField {
	var defaultCreator FieldDefaultValueCreator
	if hasDefaultValue {
		switch value := defaultValue.(type) {
		case func() interface{}:
			defaultCreator = value
		default:
			defaultCreator = func() interface{} {
				return value
			}
		}
	}

	return &BaseField{
		name:            name,
		defaultsCreator: defaultCreator,
		hasDefaultValue: hasDefaultValue,
	}
}

// DefaultsField
type DefaultsField struct {
	*BaseField
}

// Init
func (f *DefaultsField) Init(name string, container Container) (interface{}, error) {
	result, err := container.Get(name)
	switch errors.Cause(err) {
	case nil:
		// found
		break
	case define.ErrItemNotFound:
		// init item
		value, ok := f.DefaultValue()
		if !ok {
			return nil, errors.Wrapf(define.ErrDisaster, "init field %s failed", name)
		}

		err = container.Put(name, value)
		if err != nil {
			return value, err
		}
		result = value
	default:
		return nil, err
	}
	return result, nil
}

// Transform
func (f *DefaultsField) Transform(from Container, to Container) error {
	_, err := f.Init(f.name, to)
	return err
}

// NewDefaultsField
func NewDefaultsField(name string, defaults interface{}) *DefaultsField {
	return &DefaultsField{
		BaseField: NewBaseField(name, defaults, true),
	}
}

// ConstField
type ConstantField struct {
	*BaseField
}

// Transform
func (f *ConstantField) Transform(from Container, to Container) error {
	value, _ := f.DefaultValue()
	return to.Put(f.name, value)
}

// NewConstantField
func NewConstantField(name string, value interface{}) *ConstantField {
	return &ConstantField{
		BaseField: NewBaseField(name, value, true),
	}
}

// SimpleField :
type SimpleField struct {
	*BaseField
	check     CheckFn
	extract   ExtractFn
	transform TransformFn
}

// NewNewSimpleFieldWith
func NewNewSimpleFieldWith(name string, defaultValue interface{}, hasDefaultValue bool, extract ExtractFn, transform TransformFn) *SimpleField {
	return &SimpleField{
		BaseField: NewBaseField(name, defaultValue, hasDefaultValue),
		extract:   extract,
		transform: transform,
	}
}

// NewSimpleFieldWithValue :
func NewSimpleFieldWithValue(name string, defaultValue interface{}, extract ExtractFn, transform TransformFn) *SimpleField {
	return NewNewSimpleFieldWith(name, defaultValue, true, extract, transform)
}

func IfEmptyStringField(obj interface{}) bool {
	if obj == nil {
		return true
	}
	v, ok := obj.(string)
	if !ok || v == "" {
		return true
	}
	return false
}

func NewSimpleFieldWithCheck(name string, extract ExtractFn, transform TransformFn, check CheckFn) *SimpleField {
	f := NewSimpleField(name, extract, transform)
	f.check = check
	return f
}

// NewSimpleField :
func NewSimpleField(name string, extract ExtractFn, transform TransformFn) *SimpleField {
	return NewNewSimpleFieldWith(name, nil, false, extract, transform)
}

// GetValue
func (f *SimpleField) GetValue(from Container) (interface{}, error) {
	var (
		value  interface{}
		result interface{}
		err    error
	)

	if f.extract != nil {
		value, err = f.extract(from)
		if err != nil {
			logging.Warnf("%v extract error: %v, will use default instead.", f, err)
			defaults, ok := f.DefaultValue()
			if ok {
				logging.Warnf("%v extract error: %v", f, err)
				value = defaults
			} else {
				return nil, errors.WithMessagef(err, "%v extractor", f)
			}
		}
	}

	if f.transform != nil {
		result, err = f.transform(value)
		if err != nil {
			logging.Warnf("%v transform error: %v, will use default instead.", f, err)
			defaults, ok := f.DefaultValue()
			if ok {
				logging.Warnf("%s transform value `%v` error: %v", f.name, value, err)
				result = defaults
			} else {
				return nil, errors.WithMessagef(err, "%v transformer", f)
			}
		}
	} else {
		result = value
	}

	return result, nil
}

// Transform :
func (f *SimpleField) Transform(from Container, to Container) error {
	value, err := f.GetValue(from)
	if err != nil {
		return err
	}

	// 如果检查不通过 放弃更新 value
	if f.check != nil {
		if !f.check(value) {
			return nil
		}
	}

	return to.Put(f.name, value)
}

// PrepareField :
type PrepareField struct {
	*SimpleField
}

// Transform :
func (f *PrepareField) Transform(from Container, to Container) error {
	return f.SimpleField.Transform(from, from)
}

// NewPrepareField :
func NewPrepareField(name string, extract ExtractFn, transform TransformFn) *PrepareField {
	return &PrepareField{
		SimpleField: NewSimpleField(name, extract, transform),
	}
}

// InitialField
type InitialField struct {
	*SimpleField
}

// NewInitialField
func NewInitialField(name string, extract ExtractFn, transform TransformFn) *InitialField {
	return &InitialField{
		SimpleField: NewSimpleField(name, extract, transform),
	}
}

// Transform :
func (f *InitialField) Transform(from Container, to Container) error {
	_, err := to.Get(f.name)
	if err != nil {
		return f.SimpleField.Transform(from, to)
	}
	return nil
}

// FutureField :
type FutureField struct {
	*BaseField
	ready     bool
	lock      sync.Mutex
	transform func(name string, from Container, to Container) error
}

// NewFutureField :
func NewFutureField(name string, transform func(name string, from Container, to Container) error) *FutureField {
	return &FutureField{
		BaseField: NewBaseField(name, nil, false),
		transform: transform,
		ready:     false,
	}
}

// NewFutureFieldWithFn :
func NewFutureFieldWithFn(name string, transform func(name string, to Container) (interface{}, error)) *FutureField {
	return NewFutureField(name, func(n string, from Container, to Container) error {
		value, err := transform(n, to)
		if err != nil {
			return err
		}
		return to.Put(name, value)
	})
}

// FutureFieldWrap :
func FutureFieldWrap(field Field) *FutureField {
	return NewFutureField(field.String(), func(n string, from Container, to Container) error {
		return field.Transform(from, to)
	})
}

// Transform :
func (f *FutureField) Transform(from Container, to Container) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	if f.ready {
		return f.transform(f.name, from, to)
	}
	f.ready = true
	return ErrFieldNotReady
}

// FunctionField :
type FunctionField struct {
	*BaseField
	transform func(name string, from Container, to Container) error
}

// Transform :
func (f *FunctionField) Transform(from Container, to Container) error {
	return f.transform(f.String(), from, to)
}

// NewFunctionField :
func NewFunctionField(name string, transform func(name string, from Container, to Container) error) *FunctionField {
	return &FunctionField{
		BaseField: NewBaseField(name, nil, false),
		transform: transform,
	}
}

// MergeField
type MergeField struct {
	*BaseField
	field string
}

// Transform :
func (f *MergeField) Transform(from Container, to Container) error {
	values, err := from.Get(f.field)
	if err != nil {
		return err
	}

	container, ok := values.(Container)
	if !ok {
		return errors.Wrapf(define.ErrType, "unknown type %T", values)
	}

	for _, key := range container.Keys() {
		value, err := container.Get(key)
		logging.WarnIf("get value", err)
		logging.WarnIf("put value", from.Put(key, value))
	}
	return nil
}

// NewMergeField
func NewMergeField(field string) *MergeField {
	return &MergeField{
		BaseField: NewBaseField(field, nil, false),
		field:     field,
	}
}
