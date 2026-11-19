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
	"fmt"
	"time"

	"github.com/asaskevich/EventBus"
)

// Stringer :
type Stringer = fmt.Stringer

// SavePoint : transaction object
type SavePoint interface {
	Commit() error
	Reset() error
	Close() error
}

// PayloadMeta
type PayloadMeta interface {
	Load(key interface{}) (value interface{}, ok bool)
	Store(key, value interface{})
	LoadOrStore(key, value interface{}) (actual interface{}, loaded bool)
	Delete(key interface{})
	Range(f func(key, value interface{}) bool)
}

// Payload : Processor payload
type Payload interface {
	// payload to interface
	To(v interface{}) error
	// interface to payload
	From(v interface{}) error
	// sequence number
	SN() int
	// format type
	Type() string
	// Meta info
	Meta() PayloadMeta

	SetETLRecord(*ETLRecord)
	GetETLRecord() *ETLRecord

	// SetTime sets the time received
	SetTime(t time.Time)
	// GetTime gets the time received
	GetTime() time.Time

	// SetFlag 重新设置 flag
	SetFlag(flag PayloadFlag)
	// AddFlag 新增 flag
	AddFlag(flag PayloadFlag)
	// Flag 返回 payload 所有 flag
	Flag() PayloadFlag
}

type payloadCopier interface {
	copy() Payload
}

// DataProcessor : processor to handle data in pipeline
type DataProcessor interface {
	Stringer
	Process(d Payload, outputChan chan<- Payload, killChan chan<- error)
	Finish(outputChan chan<- Payload, killChan chan<- error)
	SetIndex(i int)
	Index() int
	Poll() time.Duration
	SetPoll(t time.Duration)
}

// Frontend : Processor to pull data
type Frontend interface {
	Stringer
	SavePoint
	Pull(outputChan chan<- Payload, killChan chan<- error)
	Flow() int
}

// Backend : processor to push data
type Backend interface {
	Stringer
	SavePoint
	Push(d Payload, killChan chan<- error)
	SetETLRecordFields(f *ETLRecordFields)
}

// Pipeline : pipeline to process data
type Pipeline interface {
	Stringer
	Start() <-chan error
	Stop(time.Duration) error
	Wait() error
	Flow() int
}

// Task :
type Task interface {
	Start() error
	Stop() error
	Wait() error
}

// Scheduler :
type Scheduler interface {
	Task
}

// StoreScanCallback :
type StoreScanCallback func(key string, data []byte) bool

// Store :
type Store interface {
	Exists(key string) (bool, error)
	Set(key string, data []byte, expires time.Duration) error
	Get(key string) ([]byte, error)
	Delete(key string) error
	Commit() error
	// Scan is a read only method
	Scan(prefix string, callback StoreScanCallback, withAll ...bool) error
	Close() error
	PutCache(key string, data []byte, expires time.Duration) error
	Batch() error
}

type MemStore interface {
	Store
	ScanMemData(prefix string, callback StoreScanCallback, withAll ...bool) error
}

// Service
type Service interface {
	Task

	Info(ServiceType) ([]*ServiceInfo, error)

	Enable() error
	Disable() error
	Session() Session
	EventBus() EventBus.Bus
}

// ServiceWatcher
type ServiceWatcher interface {
	Task

	Events() <-chan *WatchEvent
}

// Session :
type Session interface {
	Store
}

// ServiceWatcher
type ShadowCopier interface {
	Task

	Link(source, target string) bool
	IsLink(source, target string) bool
	Unlink(source, target string) bool
	Each(fn func(source, target string) bool)
	Sync(source string, target string) error
	SyncAll() error
}

//go:generate genny -in factory_ctx.tpl -pkg ${GOPACKAGE} -out factory_ctx_gen.go gen FT=DataProcessor,Frontend,Backend,Pipeline,Store,Scheduler
//go:generate genny -in factory.tpl -pkg ${GOPACKAGE} -out factory_gen.go gen FT=CharSetDecoder,CharSetEncoder
