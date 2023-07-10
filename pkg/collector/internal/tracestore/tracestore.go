// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tracestore

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/pkg/errors"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/cleaner"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	// TypeBuiltin 使用内置 map 存储 traces
	// 不能支持海量数据（基本上只有测试用途）
	TypeBuiltin = "builtin"

	// TypeLeveldb 使用 leveldb 存储 traces
	// 计划支持海量数据（但需要对性能进行评估）
	TypeLeveldb = "leveldb"
)

type TraceKey struct {
	TraceID pcommon.TraceID
	SpanID  pcommon.SpanID
}

func (tk TraceKey) Bytes() []byte {
	b := make([]byte, 0, 24)

	tb := tk.TraceID.Bytes()
	b = append(b, tb[:]...)
	sb := tk.SpanID.Bytes()
	b = append(b, sb[:]...)
	return b
}

func marshalTraces(traces ptrace.Traces) ([]byte, error) {
	return ptrace.NewProtoMarshaler().MarshalTraces(traces)
}

func unmarshalTraces(b []byte) (ptrace.Traces, error) {
	return ptrace.NewProtoUnmarshaler().UnmarshalTraces(b)
}

// Storage 是对存储的抽象 实现方应保证所有操作都是线程安全的
type Storage interface {
	// Name 返回 Storage 名称 `Type:DataID`
	Name() string

	Set(k TraceKey, traces ptrace.Traces) error

	// Del 删除 k 对应的键值对
	Del(k TraceKey) error

	// Get 获取 k 对应的 value
	Get(k TraceKey) (ptrace.Traces, error)

	// Clean 清理 Storage
	Clean() error
}

func init() {
	cleaner.Register("leveldb/trace_storage", CleanStorage)
}

type StorageController struct {
	dir         string
	typ         string
	mut         sync.Mutex
	leveldbStor map[int32]*leveldbStorage
	builtinStor map[int32]*builtinStorage
}

var defaultStorageController = NewStorageController(".", "")

func NewStorageController(dir, typ string) *StorageController {
	return &StorageController{
		dir:         dir,
		typ:         typ,
		leveldbStor: make(map[int32]*leveldbStorage),
		builtinStor: make(map[int32]*builtinStorage),
	}
}

func (sc *StorageController) getOrCreateLeveldbStorage(dataID int32) (*leveldbStorage, error) {
	sc.mut.Lock()
	defer sc.mut.Unlock()

	if stor, ok := sc.leveldbStor[dataID]; ok {
		return stor, nil
	}

	stor, err := newLeveldbStorage(sc.dir, dataID)
	if err != nil {
		return nil, err
	}

	sc.leveldbStor[dataID] = stor
	return stor, nil
}

func (sc *StorageController) getOrCreateBuiltinStorage(dataID int32) *builtinStorage {
	sc.mut.Lock()
	defer sc.mut.Unlock()

	if stor, ok := sc.builtinStor[dataID]; ok {
		return stor
	}

	stor := newBuiltinStorage(dataID)
	sc.builtinStor[dataID] = stor
	return stor
}

// GetOrCreate 创建对应 Storage 实例
func (sc *StorageController) GetOrCreate(dataID int32) Storage {
	var stor Storage
	var err error
	switch sc.typ {
	case TypeLeveldb:
		stor, err = sc.getOrCreateLeveldbStorage(dataID)
		if err != nil {
			logger.Errorf("failed to create leveldb storage: %v", err)
			break
		}
	}
	if stor != nil {
		return stor
	}

	// BuiltinStorage 作为兜底方案
	return sc.getOrCreateBuiltinStorage(dataID)
}

// Clean 清理所有 Storage 实例
func (sc *StorageController) Clean() error {
	sc.mut.Lock()
	defer sc.mut.Unlock()

	errs := make([]error, 0)
	for _, stor := range sc.builtinStor {
		if err := stor.Clean(); err != nil {
			errs = append(errs, err)
		}
	}
	for _, stor := range sc.leveldbStor {
		if err := stor.Clean(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// InitStorage 初始化全局 StorageController 配置
func InitStorage(dir, typ string) {
	if dir == "" {
		dir = "."
	}
	defaultStorageController.dir = dir
	defaultStorageController.typ = typ
}

// CleanStorage 清理全局 Storage
func CleanStorage() error {
	return defaultStorageController.Clean()
}

// GetOrCreateStorage 获取或创建 Storage
func GetOrCreateStorage(dataID int32) Storage {
	return defaultStorageController.GetOrCreate(dataID)
}

// builtinStorage 使用内置 map 作为存储载体
type builtinStorage struct {
	dataID int32
	mut    sync.RWMutex
	store  map[TraceKey]ptrace.Traces
}

var _ Storage = (*builtinStorage)(nil)

func newBuiltinStorage(dataID int32) *builtinStorage {
	return &builtinStorage{
		dataID: dataID,
		store:  map[TraceKey]ptrace.Traces{},
	}
}

func (bs *builtinStorage) Clean() error {
	return nil
}

func (bs *builtinStorage) Name() string {
	return fmt.Sprintf("%s:%d", TypeBuiltin, bs.dataID)
}

func (bs *builtinStorage) Get(k TraceKey) (ptrace.Traces, error) {
	bs.mut.RLock()
	defer bs.mut.RUnlock()

	v, ok := bs.store[k]
	if !ok {
		return ptrace.Traces{}, errors.New("key not found")
	}
	return v, nil
}

func (bs *builtinStorage) Set(k TraceKey, v ptrace.Traces) error {
	bs.mut.RLock()
	_, ok := bs.store[k]
	bs.mut.RUnlock()
	if ok {
		return nil
	}

	bs.mut.Lock()
	bs.store[k] = v
	bs.mut.Unlock()
	return nil
}

func (bs *builtinStorage) Del(k TraceKey) error {
	bs.mut.Lock()
	defer bs.mut.Unlock()

	delete(bs.store, k)
	return nil
}

// leveldbStorage 使用 leveldb 作为本地存储载体
type leveldbStorage struct {
	dataID int32
	db     *leveldb.DB
}

var _ Storage = (*leveldbStorage)(nil)

func newLeveldbStorage(dir string, dataID int32) (*leveldbStorage, error) {
	dir = filepath.Join(dir, fmt.Sprintf("trace_%d", dataID))
	_ = os.RemoveAll(dir)             // 清理目录
	_ = os.MkdirAll(dir, os.ModePerm) // 重建目录
	logger.Infof("leveldb storage dir: %s", dir)

	db, err := leveldb.OpenFile(dir, &opt.Options{
		NoSync:      true,
		WriteBuffer: 8 * 1024 * 1024, // 8MB
	})
	if err != nil {
		return nil, err
	}

	return &leveldbStorage{
		dataID: dataID,
		db:     db,
	}, nil
}

func (ls *leveldbStorage) Name() string {
	return fmt.Sprintf("%s:%d", TypeLeveldb, ls.dataID)
}

func (ls *leveldbStorage) Clean() error {
	if ls.db == nil {
		return nil
	}
	return ls.db.Close()
}

func (ls *leveldbStorage) Get(k TraceKey) (ptrace.Traces, error) {
	v, err := ls.db.Get(k.Bytes(), nil)
	if err != nil {
		return ptrace.Traces{}, err
	}

	return unmarshalTraces(v)
}

func (ls *leveldbStorage) Set(k TraceKey, v ptrace.Traces) error {
	b, err := marshalTraces(v)
	if err != nil {
		return err
	}

	return ls.db.Put(k.Bytes(), b, nil)
}

func (ls *leveldbStorage) Del(k TraceKey) error {
	return ls.db.Delete(k.Bytes(), nil)
}
