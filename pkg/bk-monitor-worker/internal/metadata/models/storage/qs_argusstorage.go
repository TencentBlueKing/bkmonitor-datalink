// Code generated by go-queryset. DO NOT EDIT.
package storage

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jinzhu/gorm"
)

// ===== BEGIN of all query sets

// ===== BEGIN of query set ArgusStorageQuerySet

// ArgusStorageQuerySet is an queryset type for ArgusStorage
type ArgusStorageQuerySet struct {
	db *gorm.DB
}

// NewArgusStorageQuerySet constructs new ArgusStorageQuerySet
func NewArgusStorageQuerySet(db *gorm.DB) ArgusStorageQuerySet {
	return ArgusStorageQuerySet{
		db: db.Model(&ArgusStorage{}),
	}
}

func (qs ArgusStorageQuerySet) w(db *gorm.DB) ArgusStorageQuerySet {
	return NewArgusStorageQuerySet(db)
}

func (qs ArgusStorageQuerySet) Select(fields ...ArgusStorageDBSchemaField) ArgusStorageQuerySet {
	names := []string{}
	for _, f := range fields {
		names = append(names, f.String())
	}

	return qs.w(qs.db.Select(strings.Join(names, ",")))
}

// Create is an autogenerated method
// nolint: dupl
func (o *ArgusStorage) Create(db *gorm.DB) error {
	return db.Create(o).Error
}

// Delete is an autogenerated method
// nolint: dupl
func (o *ArgusStorage) Delete(db *gorm.DB) error {
	return db.Delete(o).Error
}

// All is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) All(ret *[]ArgusStorage) error {
	return qs.db.Find(ret).Error
}

// Count is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) Count() (int, error) {
	var count int
	err := qs.db.Count(&count).Error
	return count, err
}

// Delete is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) Delete() error {
	return qs.db.Delete(ArgusStorage{}).Error
}

// DeleteNum is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) DeleteNum() (int64, error) {
	db := qs.db.Delete(ArgusStorage{})
	return db.RowsAffected, db.Error
}

// DeleteNumUnscoped is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) DeleteNumUnscoped() (int64, error) {
	db := qs.db.Unscoped().Delete(ArgusStorage{})
	return db.RowsAffected, db.Error
}

// GetDB is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) GetDB() *gorm.DB {
	return qs.db
}

// GetUpdater is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) GetUpdater() ArgusStorageUpdater {
	return NewArgusStorageUpdater(qs.db)
}

// Limit is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) Limit(limit int) ArgusStorageQuerySet {
	return qs.w(qs.db.Limit(limit))
}

// Offset is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) Offset(offset int) ArgusStorageQuerySet {
	return qs.w(qs.db.Offset(offset))
}

// One is used to retrieve one result. It returns gorm.ErrRecordNotFound
// if nothing was fetched
func (qs ArgusStorageQuerySet) One(ret *ArgusStorage) error {
	return qs.db.First(ret).Error
}

// OrderAscByStorageClusterID is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) OrderAscByStorageClusterID() ArgusStorageQuerySet {
	return qs.w(qs.db.Order("storage_cluster_id ASC"))
}

// OrderAscByTableID is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) OrderAscByTableID() ArgusStorageQuerySet {
	return qs.w(qs.db.Order("table_id ASC"))
}

// OrderAscByTenantId is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) OrderAscByTenantId() ArgusStorageQuerySet {
	return qs.w(qs.db.Order("tenant_id ASC"))
}

// OrderDescByStorageClusterID is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) OrderDescByStorageClusterID() ArgusStorageQuerySet {
	return qs.w(qs.db.Order("storage_cluster_id DESC"))
}

// OrderDescByTableID is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) OrderDescByTableID() ArgusStorageQuerySet {
	return qs.w(qs.db.Order("table_id DESC"))
}

// OrderDescByTenantId is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) OrderDescByTenantId() ArgusStorageQuerySet {
	return qs.w(qs.db.Order("tenant_id DESC"))
}

// StorageClusterIDEq is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) StorageClusterIDEq(storageClusterID uint) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("storage_cluster_id = ?", storageClusterID))
}

// StorageClusterIDGt is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) StorageClusterIDGt(storageClusterID uint) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("storage_cluster_id > ?", storageClusterID))
}

// StorageClusterIDGte is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) StorageClusterIDGte(storageClusterID uint) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("storage_cluster_id >= ?", storageClusterID))
}

// StorageClusterIDIn is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) StorageClusterIDIn(storageClusterID ...uint) ArgusStorageQuerySet {
	if len(storageClusterID) == 0 {
		qs.db.AddError(errors.New("must at least pass one storageClusterID in StorageClusterIDIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("storage_cluster_id IN (?)", storageClusterID))
}

// StorageClusterIDLt is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) StorageClusterIDLt(storageClusterID uint) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("storage_cluster_id < ?", storageClusterID))
}

// StorageClusterIDLte is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) StorageClusterIDLte(storageClusterID uint) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("storage_cluster_id <= ?", storageClusterID))
}

// StorageClusterIDNe is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) StorageClusterIDNe(storageClusterID uint) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("storage_cluster_id != ?", storageClusterID))
}

// StorageClusterIDNotIn is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) StorageClusterIDNotIn(storageClusterID ...uint) ArgusStorageQuerySet {
	if len(storageClusterID) == 0 {
		qs.db.AddError(errors.New("must at least pass one storageClusterID in StorageClusterIDNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("storage_cluster_id NOT IN (?)", storageClusterID))
}

// TableIDEq is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TableIDEq(tableID string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("table_id = ?", tableID))
}

// TableIDGt is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TableIDGt(tableID string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("table_id > ?", tableID))
}

// TableIDGte is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TableIDGte(tableID string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("table_id >= ?", tableID))
}

// TableIDIn is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TableIDIn(tableID ...string) ArgusStorageQuerySet {
	if len(tableID) == 0 {
		qs.db.AddError(errors.New("must at least pass one tableID in TableIDIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("table_id IN (?)", tableID))
}

// TableIDLike is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TableIDLike(tableID string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("table_id LIKE ?", tableID))
}

// TableIDLt is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TableIDLt(tableID string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("table_id < ?", tableID))
}

// TableIDLte is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TableIDLte(tableID string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("table_id <= ?", tableID))
}

// TableIDNe is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TableIDNe(tableID string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("table_id != ?", tableID))
}

// TableIDNotIn is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TableIDNotIn(tableID ...string) ArgusStorageQuerySet {
	if len(tableID) == 0 {
		qs.db.AddError(errors.New("must at least pass one tableID in TableIDNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("table_id NOT IN (?)", tableID))
}

// TableIDNotlike is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TableIDNotlike(tableID string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("table_id NOT LIKE ?", tableID))
}

// TenantIdEq is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TenantIdEq(tenantId string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("tenant_id = ?", tenantId))
}

// TenantIdGt is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TenantIdGt(tenantId string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("tenant_id > ?", tenantId))
}

// TenantIdGte is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TenantIdGte(tenantId string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("tenant_id >= ?", tenantId))
}

// TenantIdIn is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TenantIdIn(tenantId ...string) ArgusStorageQuerySet {
	if len(tenantId) == 0 {
		qs.db.AddError(errors.New("must at least pass one tenantId in TenantIdIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("tenant_id IN (?)", tenantId))
}

// TenantIdLike is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TenantIdLike(tenantId string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("tenant_id LIKE ?", tenantId))
}

// TenantIdLt is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TenantIdLt(tenantId string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("tenant_id < ?", tenantId))
}

// TenantIdLte is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TenantIdLte(tenantId string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("tenant_id <= ?", tenantId))
}

// TenantIdNe is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TenantIdNe(tenantId string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("tenant_id != ?", tenantId))
}

// TenantIdNotIn is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TenantIdNotIn(tenantId ...string) ArgusStorageQuerySet {
	if len(tenantId) == 0 {
		qs.db.AddError(errors.New("must at least pass one tenantId in TenantIdNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("tenant_id NOT IN (?)", tenantId))
}

// TenantIdNotlike is an autogenerated method
// nolint: dupl
func (qs ArgusStorageQuerySet) TenantIdNotlike(tenantId string) ArgusStorageQuerySet {
	return qs.w(qs.db.Where("tenant_id NOT LIKE ?", tenantId))
}

// SetStorageClusterID is an autogenerated method
// nolint: dupl
func (u ArgusStorageUpdater) SetStorageClusterID(storageClusterID uint) ArgusStorageUpdater {
	u.fields[string(ArgusStorageDBSchema.StorageClusterID)] = storageClusterID
	return u
}

// SetTableID is an autogenerated method
// nolint: dupl
func (u ArgusStorageUpdater) SetTableID(tableID string) ArgusStorageUpdater {
	u.fields[string(ArgusStorageDBSchema.TableID)] = tableID
	return u
}

// SetTenantId is an autogenerated method
// nolint: dupl
func (u ArgusStorageUpdater) SetTenantId(tenantId string) ArgusStorageUpdater {
	u.fields[string(ArgusStorageDBSchema.TenantId)] = tenantId
	return u
}

// Update is an autogenerated method
// nolint: dupl
func (u ArgusStorageUpdater) Update() error {
	return u.db.Updates(u.fields).Error
}

// UpdateNum is an autogenerated method
// nolint: dupl
func (u ArgusStorageUpdater) UpdateNum() (int64, error) {
	db := u.db.Updates(u.fields)
	return db.RowsAffected, db.Error
}

// ===== END of query set ArgusStorageQuerySet

// ===== BEGIN of ArgusStorage modifiers

// ArgusStorageDBSchemaField describes database schema field. It requires for method 'Update'
type ArgusStorageDBSchemaField string

// String method returns string representation of field.
// nolint: dupl
func (f ArgusStorageDBSchemaField) String() string {
	return string(f)
}

// ArgusStorageDBSchema stores db field names of ArgusStorage
var ArgusStorageDBSchema = struct {
	TableID          ArgusStorageDBSchemaField
	StorageClusterID ArgusStorageDBSchemaField
	TenantId         ArgusStorageDBSchemaField
}{

	TableID:          ArgusStorageDBSchemaField("table_id"),
	StorageClusterID: ArgusStorageDBSchemaField("storage_cluster_id"),
	TenantId:         ArgusStorageDBSchemaField("tenant_id"),
}

// Update updates ArgusStorage fields by primary key
// nolint: dupl
func (o *ArgusStorage) Update(db *gorm.DB, fields ...ArgusStorageDBSchemaField) error {
	dbNameToFieldName := map[string]interface{}{
		"table_id":           o.TableID,
		"storage_cluster_id": o.StorageClusterID,
		"tenant_id":          o.TenantId,
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

		return fmt.Errorf("can't update ArgusStorage %v fields %v: %s",
			o, fields, err)
	}

	return nil
}

// ArgusStorageUpdater is an ArgusStorage updates manager
type ArgusStorageUpdater struct {
	fields map[string]interface{}
	db     *gorm.DB
}

// NewArgusStorageUpdater creates new ArgusStorage updater
// nolint: dupl
func NewArgusStorageUpdater(db *gorm.DB) ArgusStorageUpdater {
	return ArgusStorageUpdater{
		fields: map[string]interface{}{},
		db:     db.Model(&ArgusStorage{}),
	}
}

// ===== END of ArgusStorage modifiers

// ===== END of all query sets
