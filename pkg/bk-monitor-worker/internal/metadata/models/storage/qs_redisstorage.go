// Code generated by go-queryset. DO NOT EDIT.
package storage

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jinzhu/gorm"
)

// ===== BEGIN of all query sets

// ===== BEGIN of query set RedisStorageQuerySet

// RedisStorageQuerySet is an queryset type for RedisStorage
type RedisStorageQuerySet struct {
	db *gorm.DB
}

// NewRedisStorageQuerySet constructs new RedisStorageQuerySet
func NewRedisStorageQuerySet(db *gorm.DB) RedisStorageQuerySet {
	return RedisStorageQuerySet{
		db: db.Model(&RedisStorage{}),
	}
}

func (qs RedisStorageQuerySet) w(db *gorm.DB) RedisStorageQuerySet {
	return NewRedisStorageQuerySet(db)
}

func (qs RedisStorageQuerySet) Select(fields ...RedisStorageDBSchemaField) RedisStorageQuerySet {
	names := []string{}
	for _, f := range fields {
		names = append(names, f.String())
	}

	return qs.w(qs.db.Select(strings.Join(names, ",")))
}

// Create is an autogenerated method
// nolint: dupl
func (o *RedisStorage) Create(db *gorm.DB) error {
	return db.Create(o).Error
}

// Delete is an autogenerated method
// nolint: dupl
func (o *RedisStorage) Delete(db *gorm.DB) error {
	return db.Delete(o).Error
}

// All is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) All(ret *[]RedisStorage) error {
	return qs.db.Find(ret).Error
}

// CommandEq is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) CommandEq(command string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("command = ?", command))
}

// CommandGt is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) CommandGt(command string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("command > ?", command))
}

// CommandGte is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) CommandGte(command string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("command >= ?", command))
}

// CommandIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) CommandIn(command ...string) RedisStorageQuerySet {
	if len(command) == 0 {
		qs.db.AddError(errors.New("must at least pass one command in CommandIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("command IN (?)", command))
}

// CommandLike is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) CommandLike(command string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("command LIKE ?", command))
}

// CommandLt is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) CommandLt(command string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("command < ?", command))
}

// CommandLte is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) CommandLte(command string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("command <= ?", command))
}

// CommandNe is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) CommandNe(command string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("command != ?", command))
}

// CommandNotIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) CommandNotIn(command ...string) RedisStorageQuerySet {
	if len(command) == 0 {
		qs.db.AddError(errors.New("must at least pass one command in CommandNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("command NOT IN (?)", command))
}

// CommandNotlike is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) CommandNotlike(command string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("command NOT LIKE ?", command))
}

// Count is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) Count() (int, error) {
	var count int
	err := qs.db.Count(&count).Error
	return count, err
}

// DBEq is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) DBEq(dB uint) RedisStorageQuerySet {
	return qs.w(qs.db.Where("db = ?", dB))
}

// DBGt is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) DBGt(dB uint) RedisStorageQuerySet {
	return qs.w(qs.db.Where("db > ?", dB))
}

// DBGte is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) DBGte(dB uint) RedisStorageQuerySet {
	return qs.w(qs.db.Where("db >= ?", dB))
}

// DBIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) DBIn(dB ...uint) RedisStorageQuerySet {
	if len(dB) == 0 {
		qs.db.AddError(errors.New("must at least pass one dB in DBIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("db IN (?)", dB))
}

// DBLt is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) DBLt(dB uint) RedisStorageQuerySet {
	return qs.w(qs.db.Where("db < ?", dB))
}

// DBLte is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) DBLte(dB uint) RedisStorageQuerySet {
	return qs.w(qs.db.Where("db <= ?", dB))
}

// DBNe is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) DBNe(dB uint) RedisStorageQuerySet {
	return qs.w(qs.db.Where("db != ?", dB))
}

// DBNotIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) DBNotIn(dB ...uint) RedisStorageQuerySet {
	if len(dB) == 0 {
		qs.db.AddError(errors.New("must at least pass one dB in DBNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("db NOT IN (?)", dB))
}

// Delete is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) Delete() error {
	return qs.db.Delete(RedisStorage{}).Error
}

// DeleteNum is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) DeleteNum() (int64, error) {
	db := qs.db.Delete(RedisStorage{})
	return db.RowsAffected, db.Error
}

// DeleteNumUnscoped is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) DeleteNumUnscoped() (int64, error) {
	db := qs.db.Unscoped().Delete(RedisStorage{})
	return db.RowsAffected, db.Error
}

// GetDB is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) GetDB() *gorm.DB {
	return qs.db
}

// GetUpdater is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) GetUpdater() RedisStorageUpdater {
	return NewRedisStorageUpdater(qs.db)
}

// IsSentinelEq is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) IsSentinelEq(isSentinel bool) RedisStorageQuerySet {
	return qs.w(qs.db.Where("is_sentinel = ?", isSentinel))
}

// IsSentinelIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) IsSentinelIn(isSentinel ...bool) RedisStorageQuerySet {
	if len(isSentinel) == 0 {
		qs.db.AddError(errors.New("must at least pass one isSentinel in IsSentinelIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("is_sentinel IN (?)", isSentinel))
}

// IsSentinelNe is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) IsSentinelNe(isSentinel bool) RedisStorageQuerySet {
	return qs.w(qs.db.Where("is_sentinel != ?", isSentinel))
}

// IsSentinelNotIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) IsSentinelNotIn(isSentinel ...bool) RedisStorageQuerySet {
	if len(isSentinel) == 0 {
		qs.db.AddError(errors.New("must at least pass one isSentinel in IsSentinelNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("is_sentinel NOT IN (?)", isSentinel))
}

// KeyEq is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) KeyEq(key string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("key = ?", key))
}

// KeyGt is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) KeyGt(key string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("key > ?", key))
}

// KeyGte is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) KeyGte(key string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("key >= ?", key))
}

// KeyIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) KeyIn(key ...string) RedisStorageQuerySet {
	if len(key) == 0 {
		qs.db.AddError(errors.New("must at least pass one key in KeyIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("key IN (?)", key))
}

// KeyLike is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) KeyLike(key string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("key LIKE ?", key))
}

// KeyLt is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) KeyLt(key string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("key < ?", key))
}

// KeyLte is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) KeyLte(key string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("key <= ?", key))
}

// KeyNe is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) KeyNe(key string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("key != ?", key))
}

// KeyNotIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) KeyNotIn(key ...string) RedisStorageQuerySet {
	if len(key) == 0 {
		qs.db.AddError(errors.New("must at least pass one key in KeyNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("key NOT IN (?)", key))
}

// KeyNotlike is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) KeyNotlike(key string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("key NOT LIKE ?", key))
}

// Limit is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) Limit(limit int) RedisStorageQuerySet {
	return qs.w(qs.db.Limit(limit))
}

// MasterNameEq is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) MasterNameEq(masterName string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("master_name = ?", masterName))
}

// MasterNameGt is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) MasterNameGt(masterName string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("master_name > ?", masterName))
}

// MasterNameGte is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) MasterNameGte(masterName string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("master_name >= ?", masterName))
}

// MasterNameIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) MasterNameIn(masterName ...string) RedisStorageQuerySet {
	if len(masterName) == 0 {
		qs.db.AddError(errors.New("must at least pass one masterName in MasterNameIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("master_name IN (?)", masterName))
}

// MasterNameLike is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) MasterNameLike(masterName string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("master_name LIKE ?", masterName))
}

// MasterNameLt is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) MasterNameLt(masterName string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("master_name < ?", masterName))
}

// MasterNameLte is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) MasterNameLte(masterName string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("master_name <= ?", masterName))
}

// MasterNameNe is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) MasterNameNe(masterName string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("master_name != ?", masterName))
}

// MasterNameNotIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) MasterNameNotIn(masterName ...string) RedisStorageQuerySet {
	if len(masterName) == 0 {
		qs.db.AddError(errors.New("must at least pass one masterName in MasterNameNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("master_name NOT IN (?)", masterName))
}

// MasterNameNotlike is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) MasterNameNotlike(masterName string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("master_name NOT LIKE ?", masterName))
}

// Offset is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) Offset(offset int) RedisStorageQuerySet {
	return qs.w(qs.db.Offset(offset))
}

// One is used to retrieve one result. It returns gorm.ErrRecordNotFound
// if nothing was fetched
func (qs RedisStorageQuerySet) One(ret *RedisStorage) error {
	return qs.db.First(ret).Error
}

// OrderAscByCommand is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderAscByCommand() RedisStorageQuerySet {
	return qs.w(qs.db.Order("command ASC"))
}

// OrderAscByDB is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderAscByDB() RedisStorageQuerySet {
	return qs.w(qs.db.Order("db ASC"))
}

// OrderAscByIsSentinel is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderAscByIsSentinel() RedisStorageQuerySet {
	return qs.w(qs.db.Order("is_sentinel ASC"))
}

// OrderAscByKey is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderAscByKey() RedisStorageQuerySet {
	return qs.w(qs.db.Order("key ASC"))
}

// OrderAscByMasterName is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderAscByMasterName() RedisStorageQuerySet {
	return qs.w(qs.db.Order("master_name ASC"))
}

// OrderAscByStorageClusterID is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderAscByStorageClusterID() RedisStorageQuerySet {
	return qs.w(qs.db.Order("storage_cluster_id ASC"))
}

// OrderAscByTableID is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderAscByTableID() RedisStorageQuerySet {
	return qs.w(qs.db.Order("table_id ASC"))
}

// OrderDescByCommand is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderDescByCommand() RedisStorageQuerySet {
	return qs.w(qs.db.Order("command DESC"))
}

// OrderDescByDB is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderDescByDB() RedisStorageQuerySet {
	return qs.w(qs.db.Order("db DESC"))
}

// OrderDescByIsSentinel is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderDescByIsSentinel() RedisStorageQuerySet {
	return qs.w(qs.db.Order("is_sentinel DESC"))
}

// OrderDescByKey is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderDescByKey() RedisStorageQuerySet {
	return qs.w(qs.db.Order("key DESC"))
}

// OrderDescByMasterName is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderDescByMasterName() RedisStorageQuerySet {
	return qs.w(qs.db.Order("master_name DESC"))
}

// OrderDescByStorageClusterID is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderDescByStorageClusterID() RedisStorageQuerySet {
	return qs.w(qs.db.Order("storage_cluster_id DESC"))
}

// OrderDescByTableID is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) OrderDescByTableID() RedisStorageQuerySet {
	return qs.w(qs.db.Order("table_id DESC"))
}

// StorageClusterIDEq is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) StorageClusterIDEq(storageClusterID uint) RedisStorageQuerySet {
	return qs.w(qs.db.Where("storage_cluster_id = ?", storageClusterID))
}

// StorageClusterIDGt is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) StorageClusterIDGt(storageClusterID uint) RedisStorageQuerySet {
	return qs.w(qs.db.Where("storage_cluster_id > ?", storageClusterID))
}

// StorageClusterIDGte is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) StorageClusterIDGte(storageClusterID uint) RedisStorageQuerySet {
	return qs.w(qs.db.Where("storage_cluster_id >= ?", storageClusterID))
}

// StorageClusterIDIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) StorageClusterIDIn(storageClusterID ...uint) RedisStorageQuerySet {
	if len(storageClusterID) == 0 {
		qs.db.AddError(errors.New("must at least pass one storageClusterID in StorageClusterIDIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("storage_cluster_id IN (?)", storageClusterID))
}

// StorageClusterIDLt is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) StorageClusterIDLt(storageClusterID uint) RedisStorageQuerySet {
	return qs.w(qs.db.Where("storage_cluster_id < ?", storageClusterID))
}

// StorageClusterIDLte is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) StorageClusterIDLte(storageClusterID uint) RedisStorageQuerySet {
	return qs.w(qs.db.Where("storage_cluster_id <= ?", storageClusterID))
}

// StorageClusterIDNe is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) StorageClusterIDNe(storageClusterID uint) RedisStorageQuerySet {
	return qs.w(qs.db.Where("storage_cluster_id != ?", storageClusterID))
}

// StorageClusterIDNotIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) StorageClusterIDNotIn(storageClusterID ...uint) RedisStorageQuerySet {
	if len(storageClusterID) == 0 {
		qs.db.AddError(errors.New("must at least pass one storageClusterID in StorageClusterIDNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("storage_cluster_id NOT IN (?)", storageClusterID))
}

// TableIDEq is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) TableIDEq(tableID string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("table_id = ?", tableID))
}

// TableIDGt is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) TableIDGt(tableID string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("table_id > ?", tableID))
}

// TableIDGte is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) TableIDGte(tableID string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("table_id >= ?", tableID))
}

// TableIDIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) TableIDIn(tableID ...string) RedisStorageQuerySet {
	if len(tableID) == 0 {
		qs.db.AddError(errors.New("must at least pass one tableID in TableIDIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("table_id IN (?)", tableID))
}

// TableIDLike is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) TableIDLike(tableID string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("table_id LIKE ?", tableID))
}

// TableIDLt is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) TableIDLt(tableID string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("table_id < ?", tableID))
}

// TableIDLte is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) TableIDLte(tableID string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("table_id <= ?", tableID))
}

// TableIDNe is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) TableIDNe(tableID string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("table_id != ?", tableID))
}

// TableIDNotIn is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) TableIDNotIn(tableID ...string) RedisStorageQuerySet {
	if len(tableID) == 0 {
		qs.db.AddError(errors.New("must at least pass one tableID in TableIDNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("table_id NOT IN (?)", tableID))
}

// TableIDNotlike is an autogenerated method
// nolint: dupl
func (qs RedisStorageQuerySet) TableIDNotlike(tableID string) RedisStorageQuerySet {
	return qs.w(qs.db.Where("table_id NOT LIKE ?", tableID))
}

// SetCommand is an autogenerated method
// nolint: dupl
func (u RedisStorageUpdater) SetCommand(command string) RedisStorageUpdater {
	u.fields[string(RedisStorageDBSchema.Command)] = command
	return u
}

// SetDB is an autogenerated method
// nolint: dupl
func (u RedisStorageUpdater) SetDB(dB uint) RedisStorageUpdater {
	u.fields[string(RedisStorageDBSchema.DB)] = dB
	return u
}

// SetIsSentinel is an autogenerated method
// nolint: dupl
func (u RedisStorageUpdater) SetIsSentinel(isSentinel bool) RedisStorageUpdater {
	u.fields[string(RedisStorageDBSchema.IsSentinel)] = isSentinel
	return u
}

// SetKey is an autogenerated method
// nolint: dupl
func (u RedisStorageUpdater) SetKey(key string) RedisStorageUpdater {
	u.fields[string(RedisStorageDBSchema.Key)] = key
	return u
}

// SetMasterName is an autogenerated method
// nolint: dupl
func (u RedisStorageUpdater) SetMasterName(masterName string) RedisStorageUpdater {
	u.fields[string(RedisStorageDBSchema.MasterName)] = masterName
	return u
}

// SetStorageClusterID is an autogenerated method
// nolint: dupl
func (u RedisStorageUpdater) SetStorageClusterID(storageClusterID uint) RedisStorageUpdater {
	u.fields[string(RedisStorageDBSchema.StorageClusterID)] = storageClusterID
	return u
}

// SetTableID is an autogenerated method
// nolint: dupl
func (u RedisStorageUpdater) SetTableID(tableID string) RedisStorageUpdater {
	u.fields[string(RedisStorageDBSchema.TableID)] = tableID
	return u
}

// Update is an autogenerated method
// nolint: dupl
func (u RedisStorageUpdater) Update() error {
	return u.db.Updates(u.fields).Error
}

// UpdateNum is an autogenerated method
// nolint: dupl
func (u RedisStorageUpdater) UpdateNum() (int64, error) {
	db := u.db.Updates(u.fields)
	return db.RowsAffected, db.Error
}

// ===== END of query set RedisStorageQuerySet

// ===== BEGIN of RedisStorage modifiers

// RedisStorageDBSchemaField describes database schema field. It requires for method 'Update'
type RedisStorageDBSchemaField string

// String method returns string representation of field.
// nolint: dupl
func (f RedisStorageDBSchemaField) String() string {
	return string(f)
}

// RedisStorageDBSchema stores db field names of RedisStorage
var RedisStorageDBSchema = struct {
	TableID          RedisStorageDBSchemaField
	Command          RedisStorageDBSchemaField
	Key              RedisStorageDBSchemaField
	DB               RedisStorageDBSchemaField
	StorageClusterID RedisStorageDBSchemaField
	IsSentinel       RedisStorageDBSchemaField
	MasterName       RedisStorageDBSchemaField
}{

	TableID:          RedisStorageDBSchemaField("table_id"),
	Command:          RedisStorageDBSchemaField("command"),
	Key:              RedisStorageDBSchemaField("key"),
	DB:               RedisStorageDBSchemaField("db"),
	StorageClusterID: RedisStorageDBSchemaField("storage_cluster_id"),
	IsSentinel:       RedisStorageDBSchemaField("is_sentinel"),
	MasterName:       RedisStorageDBSchemaField("master_name"),
}

// Update updates RedisStorage fields by primary key
// nolint: dupl
func (o *RedisStorage) Update(db *gorm.DB, fields ...RedisStorageDBSchemaField) error {
	dbNameToFieldName := map[string]interface{}{
		"table_id":           o.TableID,
		"command":            o.Command,
		"key":                o.Key,
		"db":                 o.DB,
		"storage_cluster_id": o.StorageClusterID,
		"is_sentinel":        o.IsSentinel,
		"master_name":        o.MasterName,
	}
	u := map[string]interface{}{}
	for _, f := range fields {
		fs := f.String()
		u[fs] = dbNameToFieldName[fs]
	}
	if err := db.Model(o).Updates(u).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return err
		}

		return fmt.Errorf("can't update RedisStorage %v fields %v: %s",
			o, fields, err)
	}

	return nil
}

// RedisStorageUpdater is an RedisStorage updates manager
type RedisStorageUpdater struct {
	fields map[string]interface{}
	db     *gorm.DB
}

// NewRedisStorageUpdater creates new RedisStorage updater
// nolint: dupl
func NewRedisStorageUpdater(db *gorm.DB) RedisStorageUpdater {
	return RedisStorageUpdater{
		fields: map[string]interface{}{},
		db:     db.Model(&RedisStorage{}),
	}
}

// ===== END of RedisStorage modifiers

// ===== END of all query sets
