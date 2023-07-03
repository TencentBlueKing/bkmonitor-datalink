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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

// PayloadDecoder
type PayloadDecoder struct {
	handlers []func(containers []Container) (results []Container, err error)
}

// NewPayloadDecoder
func NewPayloadDecoder() *PayloadDecoder {
	return &PayloadDecoder{
		handlers: make([]func(containers []Container) ([]Container, error), 0),
	}
}

func (d *PayloadDecoder) decode(payload define.Payload) (Container, error) {
	container := NewMapContainer()
	err := payload.To(&container)
	return container, err
}

func (d *PayloadDecoder) each(containers []Container, fn func(index int, container Container, results []Container) ([]Container, error)) (results []Container, err error) {
	results = make([]Container, 0, len(containers))
	for index, container := range containers {
		values, err := fn(index, container, results)
		if err != nil {
			return nil, err
		}
		results = values
	}
	return results, nil
}

// Register
func (d *PayloadDecoder) Register(handler func(containers []Container) (results []Container, err error)) *PayloadDecoder {
	d.handlers = append(d.handlers, handler)
	return d
}

// RegisterUpdater
func (d *PayloadDecoder) RegisterUpdater(name string, updater func(container Container) error) *PayloadDecoder {
	return d.Register(func(containers []Container) (results []Container, err error) {
		for _, container := range containers {
			err := updater(container)
			if err != nil {
				logging.Warnf("updater %s handle %#v error %s", name, container, err)
			}
		}
		return containers, nil
	})
}

// SinglePayload
func (d *PayloadDecoder) Decode(payload define.Payload) ([]Container, error) {
	container, err := d.decode(payload)
	if err != nil {
		return nil, err
	}

	containers := []Container{container}
	for _, handler := range d.handlers {
		containers, err = handler(containers)
		if err != nil {
			return nil, err
		}
	}
	return containers, nil
}

func (d *PayloadDecoder) fissionHandler(strict bool, extractor ExtractFn, fn func(index int, value interface{}, container Container) error) *PayloadDecoder {
	return d.Register(func(containers []Container) (results []Container, err error) {
		return d.each(containers, func(i int, container Container, results []Container) ([]Container, error) {
			extracted, err := extractor(container)
			if err != nil {
				if strict {
					return nil, err
				}
				return results, nil
			} else if extracted == nil {
				if !strict {
					results = append(results, container)
				}
				return results, nil
			}

			array, ok := extracted.([]interface{})
			if !ok {
				if strict {
					logging.Warnf("expect type []interface{} but got %T", extracted)
					return nil, nil
				}
				return results, nil
			}

			for index, value := range array {
				result := container.Copy()
				err = fn(index, value, result)
				if err != nil {
					logging.Warnf("fission handler error %v for %v", err, result)
					continue
				}

				results = append(results, result)
			}
			return results, nil
		})
	})
}

// FissionSplitHandler : 按指定的方式拆分
func (d *PayloadDecoder) FissionSplitHandler(strict bool, extractor ExtractFn, indexName, fieldName string) *PayloadDecoder {
	return d.fissionHandler(strict, extractor, func(index int, value interface{}, container Container) error {
		if indexName != "" {
			logging.PanicIf(container.Put(indexName, index))
		}
		logging.PanicIf(container.Put(fieldName, value))
		return nil
	})
}

// GroupSplitHandler : 按 group_info 拆分
func (d *PayloadDecoder) GroupSplitHandler(strict bool, field string) *PayloadDecoder {
	return d.FissionSplitHandler(strict, ExtractByPath(field), "", define.RecordGroupFieldName)
}

func (d *PayloadDecoder) fissionMergeHandler(strict bool, extractor ExtractFn, indexName string, fn func(container Container, items map[string]interface{}) error) *PayloadDecoder {
	return d.fissionHandler(strict, extractor, func(index int, value interface{}, container Container) error {
		items, ok := value.(map[string]interface{})
		if !ok {
			return errors.Wrapf(define.ErrType, "expect type map[string]interface{} but got %T", items)
		}

		if indexName != "" {
			logging.PanicIf(container.Put(indexName, index))
		}

		return fn(container, items)
	})
}

// FissionMergeHandler : 按指定方式合并
func (d *PayloadDecoder) FissionMergeHandler(strict bool, extractor ExtractFn, indexName string) *PayloadDecoder {
	return d.fissionMergeHandler(strict, extractor, indexName, func(container Container, items map[string]interface{}) error {
		for key, value := range items {
			logging.PanicIf(container.Put(key, value))
		}
		return nil
	})
}

// FissionMergeIntoHandler
func (d *PayloadDecoder) FissionMergeIntoHandler(strict bool, extractor ExtractFn, name string) *PayloadDecoder {
	return d.fissionMergeHandler(strict, extractor, "", func(container Container, items map[string]interface{}) error {
		dimensions, err := MakeSubContainer(container, name)
		logging.PanicIf(err)

		for key, value := range items {
			logging.PanicIf(dimensions.Put(key, value))
		}
		return nil
	})
}

// FissionMergeDimensionsHandler
func (d *PayloadDecoder) FissionMergeDimensionsHandler(strict bool, extractor ExtractFn) *PayloadDecoder {
	return d.FissionMergeIntoHandler(strict, extractor, define.RecordDimensionsFieldName)
}

// FissionMergeMetricsHandler
func (d *PayloadDecoder) FissionMergeMetricsHandler(strict bool, extractor ExtractFn) *PayloadDecoder {
	return d.FissionMergeIntoHandler(strict, extractor, define.RecordMetricsFieldName)
}
