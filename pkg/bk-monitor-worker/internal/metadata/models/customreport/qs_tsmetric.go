// Code generated by go-queryset. DO NOT EDIT.
package customreport

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jinzhu/gorm"
)

// ===== BEGIN of all query sets

// ===== BEGIN of query set TimeSeriesMetricQuerySet

// TimeSeriesMetricQuerySet is an queryset type for TimeSeriesMetric
type TimeSeriesMetricQuerySet struct {
	db *gorm.DB
}

// NewTimeSeriesMetricQuerySet constructs new TimeSeriesMetricQuerySet
func NewTimeSeriesMetricQuerySet(db *gorm.DB) TimeSeriesMetricQuerySet {
	return TimeSeriesMetricQuerySet{
		db: db.Model(&TimeSeriesMetric{}),
	}
}

func (qs TimeSeriesMetricQuerySet) w(db *gorm.DB) TimeSeriesMetricQuerySet {
	return NewTimeSeriesMetricQuerySet(db)
}

func (qs TimeSeriesMetricQuerySet) Select(fields ...TimeSeriesMetricDBSchemaField) TimeSeriesMetricQuerySet {
	names := []string{}
	for _, f := range fields {
		names = append(names, f.String())
	}

	return qs.w(qs.db.Select(strings.Join(names, ",")))
}

// Create is an autogenerated method
// nolint: dupl
func (o *TimeSeriesMetric) Create(db *gorm.DB) error {
	return db.Create(o).Error
}

// Delete is an autogenerated method
// nolint: dupl
func (o *TimeSeriesMetric) Delete(db *gorm.DB) error {
	return db.Delete(o).Error
}

// All is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) All(ret *[]TimeSeriesMetric) error {
	return qs.db.Find(ret).Error
}

// Count is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) Count() (int, error) {
	var count int
	err := qs.db.Count(&count).Error
	return count, err
}

// Delete is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) Delete() error {
	return qs.db.Delete(TimeSeriesMetric{}).Error
}

// DeleteNum is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) DeleteNum() (int64, error) {
	db := qs.db.Delete(TimeSeriesMetric{})
	return db.RowsAffected, db.Error
}

// DeleteNumUnscoped is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) DeleteNumUnscoped() (int64, error) {
	db := qs.db.Unscoped().Delete(TimeSeriesMetric{})
	return db.RowsAffected, db.Error
}

// FieldIDEq is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldIDEq(fieldID uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_id = ?", fieldID))
}

// FieldIDGt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldIDGt(fieldID uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_id > ?", fieldID))
}

// FieldIDGte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldIDGte(fieldID uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_id >= ?", fieldID))
}

// FieldIDIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldIDIn(fieldID ...uint) TimeSeriesMetricQuerySet {
	if len(fieldID) == 0 {
		qs.db.AddError(errors.New("must at least pass one fieldID in FieldIDIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("field_id IN (?)", fieldID))
}

// FieldIDLt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldIDLt(fieldID uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_id < ?", fieldID))
}

// FieldIDLte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldIDLte(fieldID uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_id <= ?", fieldID))
}

// FieldIDNe is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldIDNe(fieldID uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_id != ?", fieldID))
}

// FieldIDNotIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldIDNotIn(fieldID ...uint) TimeSeriesMetricQuerySet {
	if len(fieldID) == 0 {
		qs.db.AddError(errors.New("must at least pass one fieldID in FieldIDNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("field_id NOT IN (?)", fieldID))
}

// FieldNameEq is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldNameEq(fieldName string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_name = ?", fieldName))
}

// FieldNameGt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldNameGt(fieldName string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_name > ?", fieldName))
}

// FieldNameGte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldNameGte(fieldName string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_name >= ?", fieldName))
}

// FieldNameIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldNameIn(fieldName ...string) TimeSeriesMetricQuerySet {
	if len(fieldName) == 0 {
		qs.db.AddError(errors.New("must at least pass one fieldName in FieldNameIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("field_name IN (?)", fieldName))
}

// FieldNameLike is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldNameLike(fieldName string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_name LIKE ?", fieldName))
}

// FieldNameLt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldNameLt(fieldName string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_name < ?", fieldName))
}

// FieldNameLte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldNameLte(fieldName string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_name <= ?", fieldName))
}

// FieldNameNe is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldNameNe(fieldName string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_name != ?", fieldName))
}

// FieldNameNotIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldNameNotIn(fieldName ...string) TimeSeriesMetricQuerySet {
	if len(fieldName) == 0 {
		qs.db.AddError(errors.New("must at least pass one fieldName in FieldNameNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("field_name NOT IN (?)", fieldName))
}

// FieldNameNotlike is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) FieldNameNotlike(fieldName string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("field_name NOT LIKE ?", fieldName))
}

// GetDB is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) GetDB() *gorm.DB {
	return qs.db
}

// GetUpdater is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) GetUpdater() TimeSeriesMetricUpdater {
	return NewTimeSeriesMetricUpdater(qs.db)
}

// GroupIDEq is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) GroupIDEq(groupID uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("group_id = ?", groupID))
}

// GroupIDGt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) GroupIDGt(groupID uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("group_id > ?", groupID))
}

// GroupIDGte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) GroupIDGte(groupID uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("group_id >= ?", groupID))
}

// GroupIDIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) GroupIDIn(groupID ...uint) TimeSeriesMetricQuerySet {
	if len(groupID) == 0 {
		qs.db.AddError(errors.New("must at least pass one groupID in GroupIDIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("group_id IN (?)", groupID))
}

// GroupIDLt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) GroupIDLt(groupID uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("group_id < ?", groupID))
}

// GroupIDLte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) GroupIDLte(groupID uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("group_id <= ?", groupID))
}

// GroupIDNe is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) GroupIDNe(groupID uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("group_id != ?", groupID))
}

// GroupIDNotIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) GroupIDNotIn(groupID ...uint) TimeSeriesMetricQuerySet {
	if len(groupID) == 0 {
		qs.db.AddError(errors.New("must at least pass one groupID in GroupIDNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("group_id NOT IN (?)", groupID))
}

// LabelEq is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LabelEq(label string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("label = ?", label))
}

// LabelGt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LabelGt(label string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("label > ?", label))
}

// LabelGte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LabelGte(label string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("label >= ?", label))
}

// LabelIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LabelIn(label ...string) TimeSeriesMetricQuerySet {
	if len(label) == 0 {
		qs.db.AddError(errors.New("must at least pass one label in LabelIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("label IN (?)", label))
}

// LabelLike is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LabelLike(label string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("label LIKE ?", label))
}

// LabelLt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LabelLt(label string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("label < ?", label))
}

// LabelLte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LabelLte(label string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("label <= ?", label))
}

// LabelNe is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LabelNe(label string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("label != ?", label))
}

// LabelNotIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LabelNotIn(label ...string) TimeSeriesMetricQuerySet {
	if len(label) == 0 {
		qs.db.AddError(errors.New("must at least pass one label in LabelNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("label NOT IN (?)", label))
}

// LabelNotlike is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LabelNotlike(label string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("label NOT LIKE ?", label))
}

// LastIndexEq is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastIndexEq(lastIndex uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("last_index = ?", lastIndex))
}

// LastIndexGt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastIndexGt(lastIndex uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("last_index > ?", lastIndex))
}

// LastIndexGte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastIndexGte(lastIndex uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("last_index >= ?", lastIndex))
}

// LastIndexIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastIndexIn(lastIndex ...uint) TimeSeriesMetricQuerySet {
	if len(lastIndex) == 0 {
		qs.db.AddError(errors.New("must at least pass one lastIndex in LastIndexIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("last_index IN (?)", lastIndex))
}

// LastIndexLt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastIndexLt(lastIndex uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("last_index < ?", lastIndex))
}

// LastIndexLte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastIndexLte(lastIndex uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("last_index <= ?", lastIndex))
}

// LastIndexNe is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastIndexNe(lastIndex uint) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("last_index != ?", lastIndex))
}

// LastIndexNotIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastIndexNotIn(lastIndex ...uint) TimeSeriesMetricQuerySet {
	if len(lastIndex) == 0 {
		qs.db.AddError(errors.New("must at least pass one lastIndex in LastIndexNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("last_index NOT IN (?)", lastIndex))
}

// LastModifyTimeEq is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastModifyTimeEq(lastModifyTime time.Time) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("last_modify_time = ?", lastModifyTime))
}

// LastModifyTimeGt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastModifyTimeGt(lastModifyTime time.Time) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("last_modify_time > ?", lastModifyTime))
}

// LastModifyTimeGte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastModifyTimeGte(lastModifyTime time.Time) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("last_modify_time >= ?", lastModifyTime))
}

// LastModifyTimeLt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastModifyTimeLt(lastModifyTime time.Time) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("last_modify_time < ?", lastModifyTime))
}

// LastModifyTimeLte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastModifyTimeLte(lastModifyTime time.Time) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("last_modify_time <= ?", lastModifyTime))
}

// LastModifyTimeNe is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) LastModifyTimeNe(lastModifyTime time.Time) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("last_modify_time != ?", lastModifyTime))
}

// Limit is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) Limit(limit int) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Limit(limit))
}

// Offset is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) Offset(offset int) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Offset(offset))
}

// One is used to retrieve one result. It returns gorm.ErrRecordNotFound
// if nothing was fetched
func (qs TimeSeriesMetricQuerySet) One(ret *TimeSeriesMetric) error {
	return qs.db.First(ret).Error
}

// OrderAscByFieldID is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderAscByFieldID() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("field_id ASC"))
}

// OrderAscByFieldName is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderAscByFieldName() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("field_name ASC"))
}

// OrderAscByGroupID is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderAscByGroupID() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("group_id ASC"))
}

// OrderAscByLabel is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderAscByLabel() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("label ASC"))
}

// OrderAscByLastIndex is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderAscByLastIndex() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("last_index ASC"))
}

// OrderAscByLastModifyTime is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderAscByLastModifyTime() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("last_modify_time ASC"))
}

// OrderAscByTableID is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderAscByTableID() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("table_id ASC"))
}

// OrderAscByTagList is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderAscByTagList() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("tag_list ASC"))
}

// OrderDescByFieldID is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderDescByFieldID() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("field_id DESC"))
}

// OrderDescByFieldName is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderDescByFieldName() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("field_name DESC"))
}

// OrderDescByGroupID is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderDescByGroupID() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("group_id DESC"))
}

// OrderDescByLabel is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderDescByLabel() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("label DESC"))
}

// OrderDescByLastIndex is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderDescByLastIndex() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("last_index DESC"))
}

// OrderDescByLastModifyTime is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderDescByLastModifyTime() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("last_modify_time DESC"))
}

// OrderDescByTableID is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderDescByTableID() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("table_id DESC"))
}

// OrderDescByTagList is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) OrderDescByTagList() TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Order("tag_list DESC"))
}

// TableIDEq is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TableIDEq(tableID string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("table_id = ?", tableID))
}

// TableIDGt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TableIDGt(tableID string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("table_id > ?", tableID))
}

// TableIDGte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TableIDGte(tableID string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("table_id >= ?", tableID))
}

// TableIDIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TableIDIn(tableID ...string) TimeSeriesMetricQuerySet {
	if len(tableID) == 0 {
		qs.db.AddError(errors.New("must at least pass one tableID in TableIDIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("table_id IN (?)", tableID))
}

// TableIDLike is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TableIDLike(tableID string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("table_id LIKE ?", tableID))
}

// TableIDLt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TableIDLt(tableID string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("table_id < ?", tableID))
}

// TableIDLte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TableIDLte(tableID string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("table_id <= ?", tableID))
}

// TableIDNe is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TableIDNe(tableID string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("table_id != ?", tableID))
}

// TableIDNotIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TableIDNotIn(tableID ...string) TimeSeriesMetricQuerySet {
	if len(tableID) == 0 {
		qs.db.AddError(errors.New("must at least pass one tableID in TableIDNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("table_id NOT IN (?)", tableID))
}

// TableIDNotlike is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TableIDNotlike(tableID string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("table_id NOT LIKE ?", tableID))
}

// TagListEq is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TagListEq(tagList string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("tag_list = ?", tagList))
}

// TagListGt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TagListGt(tagList string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("tag_list > ?", tagList))
}

// TagListGte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TagListGte(tagList string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("tag_list >= ?", tagList))
}

// TagListIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TagListIn(tagList ...string) TimeSeriesMetricQuerySet {
	if len(tagList) == 0 {
		qs.db.AddError(errors.New("must at least pass one tagList in TagListIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("tag_list IN (?)", tagList))
}

// TagListLike is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TagListLike(tagList string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("tag_list LIKE ?", tagList))
}

// TagListLt is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TagListLt(tagList string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("tag_list < ?", tagList))
}

// TagListLte is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TagListLte(tagList string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("tag_list <= ?", tagList))
}

// TagListNe is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TagListNe(tagList string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("tag_list != ?", tagList))
}

// TagListNotIn is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TagListNotIn(tagList ...string) TimeSeriesMetricQuerySet {
	if len(tagList) == 0 {
		qs.db.AddError(errors.New("must at least pass one tagList in TagListNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("tag_list NOT IN (?)", tagList))
}

// TagListNotlike is an autogenerated method
// nolint: dupl
func (qs TimeSeriesMetricQuerySet) TagListNotlike(tagList string) TimeSeriesMetricQuerySet {
	return qs.w(qs.db.Where("tag_list NOT LIKE ?", tagList))
}

// SetFieldID is an autogenerated method
// nolint: dupl
func (u TimeSeriesMetricUpdater) SetFieldID(fieldID uint) TimeSeriesMetricUpdater {
	u.fields[string(TimeSeriesMetricDBSchema.FieldID)] = fieldID
	return u
}

// SetFieldName is an autogenerated method
// nolint: dupl
func (u TimeSeriesMetricUpdater) SetFieldName(fieldName string) TimeSeriesMetricUpdater {
	u.fields[string(TimeSeriesMetricDBSchema.FieldName)] = fieldName
	return u
}

// SetGroupID is an autogenerated method
// nolint: dupl
func (u TimeSeriesMetricUpdater) SetGroupID(groupID uint) TimeSeriesMetricUpdater {
	u.fields[string(TimeSeriesMetricDBSchema.GroupID)] = groupID
	return u
}

// SetLabel is an autogenerated method
// nolint: dupl
func (u TimeSeriesMetricUpdater) SetLabel(label string) TimeSeriesMetricUpdater {
	u.fields[string(TimeSeriesMetricDBSchema.Label)] = label
	return u
}

// SetLastIndex is an autogenerated method
// nolint: dupl
func (u TimeSeriesMetricUpdater) SetLastIndex(lastIndex uint) TimeSeriesMetricUpdater {
	u.fields[string(TimeSeriesMetricDBSchema.LastIndex)] = lastIndex
	return u
}

// SetLastModifyTime is an autogenerated method
// nolint: dupl
func (u TimeSeriesMetricUpdater) SetLastModifyTime(lastModifyTime time.Time) TimeSeriesMetricUpdater {
	u.fields[string(TimeSeriesMetricDBSchema.LastModifyTime)] = lastModifyTime
	return u
}

// SetTableID is an autogenerated method
// nolint: dupl
func (u TimeSeriesMetricUpdater) SetTableID(tableID string) TimeSeriesMetricUpdater {
	u.fields[string(TimeSeriesMetricDBSchema.TableID)] = tableID
	return u
}

// SetTagList is an autogenerated method
// nolint: dupl
func (u TimeSeriesMetricUpdater) SetTagList(tagList string) TimeSeriesMetricUpdater {
	u.fields[string(TimeSeriesMetricDBSchema.TagList)] = tagList
	return u
}

// Update is an autogenerated method
// nolint: dupl
func (u TimeSeriesMetricUpdater) Update() error {
	return u.db.Updates(u.fields).Error
}

// UpdateNum is an autogenerated method
// nolint: dupl
func (u TimeSeriesMetricUpdater) UpdateNum() (int64, error) {
	db := u.db.Updates(u.fields)
	return db.RowsAffected, db.Error
}

// ===== END of query set TimeSeriesMetricQuerySet

// ===== BEGIN of TimeSeriesMetric modifiers

// TimeSeriesMetricDBSchemaField describes database schema field. It requires for method 'Update'
type TimeSeriesMetricDBSchemaField string

// String method returns string representation of field.
// nolint: dupl
func (f TimeSeriesMetricDBSchemaField) String() string {
	return string(f)
}

// TimeSeriesMetricDBSchema stores db field names of TimeSeriesMetric
var TimeSeriesMetricDBSchema = struct {
	GroupID        TimeSeriesMetricDBSchemaField
	TableID        TimeSeriesMetricDBSchemaField
	FieldID        TimeSeriesMetricDBSchemaField
	FieldName      TimeSeriesMetricDBSchemaField
	TagList        TimeSeriesMetricDBSchemaField
	LastModifyTime TimeSeriesMetricDBSchemaField
	LastIndex      TimeSeriesMetricDBSchemaField
	Label          TimeSeriesMetricDBSchemaField
}{

	GroupID:        TimeSeriesMetricDBSchemaField("group_id"),
	TableID:        TimeSeriesMetricDBSchemaField("table_id"),
	FieldID:        TimeSeriesMetricDBSchemaField("field_id"),
	FieldName:      TimeSeriesMetricDBSchemaField("field_name"),
	TagList:        TimeSeriesMetricDBSchemaField("tag_list"),
	LastModifyTime: TimeSeriesMetricDBSchemaField("last_modify_time"),
	LastIndex:      TimeSeriesMetricDBSchemaField("last_index"),
	Label:          TimeSeriesMetricDBSchemaField("label"),
}

// Update updates TimeSeriesMetric fields by primary key
// nolint: dupl
func (o *TimeSeriesMetric) Update(db *gorm.DB, fields ...TimeSeriesMetricDBSchemaField) error {
	dbNameToFieldName := map[string]interface{}{
		"group_id":         o.GroupID,
		"table_id":         o.TableID,
		"field_id":         o.FieldID,
		"field_name":       o.FieldName,
		"tag_list":         o.TagList,
		"last_modify_time": o.LastModifyTime,
		"last_index":       o.LastIndex,
		"label":            o.Label,
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

		return fmt.Errorf("can't update TimeSeriesMetric %v fields %v: %s",
			o, fields, err)
	}

	return nil
}

// TimeSeriesMetricUpdater is an TimeSeriesMetric updates manager
type TimeSeriesMetricUpdater struct {
	fields map[string]interface{}
	db     *gorm.DB
}

// NewTimeSeriesMetricUpdater creates new TimeSeriesMetric updater
// nolint: dupl
func NewTimeSeriesMetricUpdater(db *gorm.DB) TimeSeriesMetricUpdater {
	return TimeSeriesMetricUpdater{
		fields: map[string]interface{}{},
		db:     db.Model(&TimeSeriesMetric{}),
	}
}

// ===== END of TimeSeriesMetric modifiers

// ===== END of all query sets
