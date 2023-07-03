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
	"strings"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// ContainerSchema : combine some records as a structure
type ContainerSchema struct {
	*DefaultsField
	records []Record
}

func (s *ContainerSchema) getContainerByPath(path string, root Container) (Container, error) {
	current := root
	for _, name := range strings.Split(path, ".") {
		if name == "" {
			continue
		}
		value, err := s.Init(name, current)
		if err != nil {
			return nil, errors.WithMessagef(err, "init container for %v", name)
		}
		container, ok := value.(Container) // should be a container
		if !ok {
			return nil, errors.WithMessagef(define.ErrType, "type of %v is %T", name, value)
		}
		current = container
	}
	return current, nil
}

// Transform
func (s *ContainerSchema) Transform(from Container, to Container) error {
	for _, record := range s.records {
		var container Container
		name := record.Name()
		container, err := s.getContainerByPath(name, to)
		if err != nil {
			return errors.WithMessagef(err, "init container for %v", name)
		}

		err = record.Transform(from, container)
		if err != nil {
			return errors.WithMessagef(err, "record %v", name)
		}
	}

	errs := utils.NewMultiErrors()
	for _, record := range s.records {
		errs.Add(record.Finish())
	}
	return errs.AsError()
}

// NewContainerSchema
func NewContainerSchema(name string, containerCreator func() Container, records []Record) *ContainerSchema {
	return &ContainerSchema{
		DefaultsField: NewDefaultsField(name, func() interface{} {
			return containerCreator()
		}),
		records: records,
	}
}

// NewDefaultContainerSchema
func NewDefaultContainerSchema(name string, records []Record) *ContainerSchema {
	return NewContainerSchema(name, func() Container {
		return NewMapContainer()
	}, records)
}

// ContainerSchemaBuilderPlugin
type ContainerSchemaBuilderPlugin func(builder *ContainerSchemaBuilder) error

// ContainerSchemaBuilder
type ContainerSchemaBuilder struct {
	Name             string
	ContainerCreator func() Container
	Records          []Record
}

// GetRecord
func (b *ContainerSchemaBuilder) GetRecord(name string) Record {
	for _, record := range b.Records {
		if record.Name() == name {
			return record
		}
	}
	return nil
}

// AddRecords
func (b *ContainerSchemaBuilder) AddRecords(records ...Record) {
	b.Records = append(b.Records, records...)
}

// Apply
func (b *ContainerSchemaBuilder) Apply(plugins ...ContainerSchemaBuilderPlugin) error {
	for _, plugin := range plugins {
		err := plugin(b)
		if err != nil {
			return err
		}
	}
	return nil
}

// Finish
func (b *ContainerSchemaBuilder) Finish() *ContainerSchema {
	return NewContainerSchema(b.Name, b.ContainerCreator, b.Records)
}

// NewContainerSchemaBuilder
func NewContainerSchemaBuilder() *ContainerSchemaBuilder {
	return &ContainerSchemaBuilder{
		ContainerCreator: func() Container {
			return NewMapContainer()
		},
		Records: make([]Record, 0),
	}
}
