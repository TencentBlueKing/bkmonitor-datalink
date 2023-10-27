// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package labelstore

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/pkg/errors"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/cleaner"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/labels"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	// TypeBuiltin 使用内置 map 数据类型作为存储载体 并通过读写锁提高并发效率（默认）
	// 优点：读写性能最好
	// 缺点：耗内存 无法承受太大的数量级
	// 建议：在数据量低于千万级的场景使用
	TypeBuiltin = "builtin"

	// TypeLeveldb 使用 leveldb 作为本地存储载体
	// 优点：资源消耗较小 可以承受千万量级甚至更高的量
	// 缺点：读写性能较差
	// 建议：在数据量超过千万级的场景使用
	TypeLeveldb = "leveldb"
)

// Storage 是对存储的抽象 实现方应保证所有操作都是线程安全的
type Storage interface {
	// Name 返回 Storage 名称 `Type:ID`
	Name() string

	// SetIf 更新 k 所对应的 labels 如果 k 已经存在 则不做任何处理
	SetIf(k uint64, lbs labels.Labels) error

	// Del 删除 k 对应的键值对
	Del(k uint64) error

	// Get 获取 k 对应的 value
	Get(k uint64) (labels.Labels, error)

	// Clean 清理 Storage
	Clean() error
}

var ErrKeyNotFound = errors.New("key not found")

func uint64ToBytes(n uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	return b
}

func init() {
	cleaner.Register("leveldb/label_storage", CleanStorage)
}

type StorageController struct {
	dir         string
	typ         string
	mut         sync.Mutex
	leveldbStor map[string]*leveldbStorage
	builtinStor map[string]*builtinStorage
}

var defaultStorageController = NewStorageController(".", "")

func NewStorageController(dir, typ string) *StorageController {
	return &StorageController{
		dir:         dir,
		typ:         typ,
		leveldbStor: make(map[string]*leveldbStorage),
		builtinStor: make(map[string]*builtinStorage),
	}
}

func (sc *StorageController) getOrCreateLeveldbStorage(id string) (*leveldbStorage, error) {
	sc.mut.Lock()
	defer sc.mut.Unlock()

	if stor, ok := sc.leveldbStor[id]; ok {
		return stor, nil
	}

	stor, err := newLeveldbStorage(sc.dir, id)
	if err != nil {
		return nil, err
	}

	sc.leveldbStor[id] = stor
	return stor, nil
}

func (sc *StorageController) getOrCreateBuiltinStorage(id string) *builtinStorage {
	sc.mut.Lock()
	defer sc.mut.Unlock()

	if stor, ok := sc.builtinStor[id]; ok {
		return stor
	}

	stor := newBuiltinStorage(id)
	sc.builtinStor[id] = stor
	return stor
}

// GetOrCreate 创建对应 Storage 实例
func (sc *StorageController) GetOrCreate(id string) Storage {
	var stor Storage
	var err error
	switch sc.typ {
	case TypeLeveldb:
		stor, err = sc.getOrCreateLeveldbStorage(id)
		if err != nil {
			logger.Errorf("failed to create leveldb storage, storid=%s, err: %v", id, err)
			break
		}
	}
	if stor != nil {
		return stor
	}

	// BuiltinStorage 作为兜底方案
	return sc.getOrCreateBuiltinStorage(id)
}

// Remove 清理 Storage 实例
func (sc *StorageController) Remove(id string) {
	sc.mut.Lock()
	defer sc.mut.Unlock()

	delete(sc.builtinStor, id)
	delete(sc.leveldbStor, id)
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
func GetOrCreateStorage(id string) Storage {
	return defaultStorageController.GetOrCreate(id)
}

// RemoveStorage 清理 Storage 实例
func RemoveStorage(id string) {
	defaultStorageController.Remove(id)
}

// builtinStorage 使用内置 map 作为存储载体
type builtinStorage struct {
	id    string
	mut   sync.RWMutex
	store map[uint64]labels.Labels
}

var _ Storage = (*builtinStorage)(nil)

func newBuiltinStorage(id string) *builtinStorage {
	return &builtinStorage{
		id:    id,
		store: map[uint64]labels.Labels{},
	}
}

func (bs *builtinStorage) Clean() error {
	bs.mut.Lock()
	defer bs.mut.Unlock()

	bs.store = map[uint64]labels.Labels{}
	return nil
}

func (bs *builtinStorage) Name() string {
	return fmt.Sprintf("%s:%s", TypeBuiltin, bs.id)
}

func (bs *builtinStorage) Get(k uint64) (labels.Labels, error) {
	bs.mut.RLock()
	defer bs.mut.RUnlock()

	v, ok := bs.store[k]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return v, nil
}

func (bs *builtinStorage) SetIf(k uint64, v labels.Labels) error {
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

func (bs *builtinStorage) Del(k uint64) error {
	bs.mut.Lock()
	defer bs.mut.Unlock()

	delete(bs.store, k)
	return nil
}

// leveldbStorage 使用 leveldb 作为本地存储载体
type leveldbStorage struct {
	id string
	db *leveldb.DB
}

var _ Storage = (*leveldbStorage)(nil)

func newLeveldbStorage(dir, id string) (*leveldbStorage, error) {
	dir = filepath.Join(dir, fmt.Sprintf("label_%s", id))
	_ = os.RemoveAll(dir)             // 清理目录
	_ = os.MkdirAll(dir, os.ModePerm) // 重建目录
	logger.Infof("leveldb storage dir: %s", dir)

	db, err := leveldb.OpenFile(dir, &opt.Options{NoSync: true})
	if err != nil {
		return nil, err
	}

	return &leveldbStorage{
		id: id,
		db: db,
	}, nil
}

func (ls *leveldbStorage) Name() string {
	return fmt.Sprintf("%s:%s", TypeLeveldb, ls.id)
}

func (ls *leveldbStorage) Clean() error {
	if ls.db == nil {
		return nil
	}
	return ls.db.Close()
}

func (ls *leveldbStorage) Get(k uint64) (labels.Labels, error) {
	v, err := ls.db.Get(uint64ToBytes(k), nil)
	if err != nil {
		return nil, err
	}

	var lbs labels.Labels
	if _, err := lbs.UnmarshalMsg(v); err != nil {
		return nil, err
	}
	return lbs, nil
}

func (ls *leveldbStorage) SetIf(k uint64, v labels.Labels) error {
	_, err := ls.db.Get(uint64ToBytes(k), nil)
	if err == nil {
		// 表明 k 存在
		return nil
	}

	b, err := v.MarshalMsg(nil)
	if err != nil {
		return err
	}

	return ls.db.Put(uint64ToBytes(k), b, nil)
}

func (ls *leveldbStorage) Del(k uint64) error {
	return ls.db.Delete(uint64ToBytes(k), nil)
}
