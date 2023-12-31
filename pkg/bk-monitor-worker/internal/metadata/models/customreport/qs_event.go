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

// ===== BEGIN of query set EventQuerySet

// EventQuerySet is an queryset type for Event
type EventQuerySet struct {
	db *gorm.DB
}

// NewEventQuerySet constructs new EventQuerySet
func NewEventQuerySet(db *gorm.DB) EventQuerySet {
	return EventQuerySet{
		db: db.Model(&Event{}),
	}
}

func (qs EventQuerySet) w(db *gorm.DB) EventQuerySet {
	return NewEventQuerySet(db)
}

func (qs EventQuerySet) Select(fields ...EventDBSchemaField) EventQuerySet {
	names := []string{}
	for _, f := range fields {
		names = append(names, f.String())
	}

	return qs.w(qs.db.Select(strings.Join(names, ",")))
}

// Create is an autogenerated method
// nolint: dupl
func (o *Event) Create(db *gorm.DB) error {
	return db.Create(o).Error
}

// Delete is an autogenerated method
// nolint: dupl
func (o *Event) Delete(db *gorm.DB) error {
	return db.Delete(o).Error
}

// All is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) All(ret *[]Event) error {
	return qs.db.Find(ret).Error
}

// Count is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) Count() (int, error) {
	var count int
	err := qs.db.Count(&count).Error
	return count, err
}

// Delete is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) Delete() error {
	return qs.db.Delete(Event{}).Error
}

// DeleteNum is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) DeleteNum() (int64, error) {
	db := qs.db.Delete(Event{})
	return db.RowsAffected, db.Error
}

// DeleteNumUnscoped is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) DeleteNumUnscoped() (int64, error) {
	db := qs.db.Unscoped().Delete(Event{})
	return db.RowsAffected, db.Error
}

// DimensionListEq is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) DimensionListEq(dimensionList string) EventQuerySet {
	return qs.w(qs.db.Where("dimension_list = ?", dimensionList))
}

// DimensionListGt is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) DimensionListGt(dimensionList string) EventQuerySet {
	return qs.w(qs.db.Where("dimension_list > ?", dimensionList))
}

// DimensionListGte is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) DimensionListGte(dimensionList string) EventQuerySet {
	return qs.w(qs.db.Where("dimension_list >= ?", dimensionList))
}

// DimensionListIn is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) DimensionListIn(dimensionList ...string) EventQuerySet {
	if len(dimensionList) == 0 {
		qs.db.AddError(errors.New("must at least pass one dimensionList in DimensionListIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("dimension_list IN (?)", dimensionList))
}

// DimensionListLike is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) DimensionListLike(dimensionList string) EventQuerySet {
	return qs.w(qs.db.Where("dimension_list LIKE ?", dimensionList))
}

// DimensionListLt is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) DimensionListLt(dimensionList string) EventQuerySet {
	return qs.w(qs.db.Where("dimension_list < ?", dimensionList))
}

// DimensionListLte is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) DimensionListLte(dimensionList string) EventQuerySet {
	return qs.w(qs.db.Where("dimension_list <= ?", dimensionList))
}

// DimensionListNe is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) DimensionListNe(dimensionList string) EventQuerySet {
	return qs.w(qs.db.Where("dimension_list != ?", dimensionList))
}

// DimensionListNotIn is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) DimensionListNotIn(dimensionList ...string) EventQuerySet {
	if len(dimensionList) == 0 {
		qs.db.AddError(errors.New("must at least pass one dimensionList in DimensionListNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("dimension_list NOT IN (?)", dimensionList))
}

// DimensionListNotlike is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) DimensionListNotlike(dimensionList string) EventQuerySet {
	return qs.w(qs.db.Where("dimension_list NOT LIKE ?", dimensionList))
}

// EventGroupIDEq is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventGroupIDEq(eventGroupID uint) EventQuerySet {
	return qs.w(qs.db.Where("event_group_id = ?", eventGroupID))
}

// EventGroupIDGt is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventGroupIDGt(eventGroupID uint) EventQuerySet {
	return qs.w(qs.db.Where("event_group_id > ?", eventGroupID))
}

// EventGroupIDGte is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventGroupIDGte(eventGroupID uint) EventQuerySet {
	return qs.w(qs.db.Where("event_group_id >= ?", eventGroupID))
}

// EventGroupIDIn is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventGroupIDIn(eventGroupID ...uint) EventQuerySet {
	if len(eventGroupID) == 0 {
		qs.db.AddError(errors.New("must at least pass one eventGroupID in EventGroupIDIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("event_group_id IN (?)", eventGroupID))
}

// EventGroupIDLt is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventGroupIDLt(eventGroupID uint) EventQuerySet {
	return qs.w(qs.db.Where("event_group_id < ?", eventGroupID))
}

// EventGroupIDLte is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventGroupIDLte(eventGroupID uint) EventQuerySet {
	return qs.w(qs.db.Where("event_group_id <= ?", eventGroupID))
}

// EventGroupIDNe is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventGroupIDNe(eventGroupID uint) EventQuerySet {
	return qs.w(qs.db.Where("event_group_id != ?", eventGroupID))
}

// EventGroupIDNotIn is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventGroupIDNotIn(eventGroupID ...uint) EventQuerySet {
	if len(eventGroupID) == 0 {
		qs.db.AddError(errors.New("must at least pass one eventGroupID in EventGroupIDNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("event_group_id NOT IN (?)", eventGroupID))
}

// EventIDEq is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventIDEq(eventID uint) EventQuerySet {
	return qs.w(qs.db.Where("event_id = ?", eventID))
}

// EventIDGt is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventIDGt(eventID uint) EventQuerySet {
	return qs.w(qs.db.Where("event_id > ?", eventID))
}

// EventIDGte is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventIDGte(eventID uint) EventQuerySet {
	return qs.w(qs.db.Where("event_id >= ?", eventID))
}

// EventIDIn is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventIDIn(eventID ...uint) EventQuerySet {
	if len(eventID) == 0 {
		qs.db.AddError(errors.New("must at least pass one eventID in EventIDIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("event_id IN (?)", eventID))
}

// EventIDLt is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventIDLt(eventID uint) EventQuerySet {
	return qs.w(qs.db.Where("event_id < ?", eventID))
}

// EventIDLte is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventIDLte(eventID uint) EventQuerySet {
	return qs.w(qs.db.Where("event_id <= ?", eventID))
}

// EventIDNe is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventIDNe(eventID uint) EventQuerySet {
	return qs.w(qs.db.Where("event_id != ?", eventID))
}

// EventIDNotIn is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventIDNotIn(eventID ...uint) EventQuerySet {
	if len(eventID) == 0 {
		qs.db.AddError(errors.New("must at least pass one eventID in EventIDNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("event_id NOT IN (?)", eventID))
}

// EventNameEq is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventNameEq(eventName string) EventQuerySet {
	return qs.w(qs.db.Where("event_name = ?", eventName))
}

// EventNameGt is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventNameGt(eventName string) EventQuerySet {
	return qs.w(qs.db.Where("event_name > ?", eventName))
}

// EventNameGte is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventNameGte(eventName string) EventQuerySet {
	return qs.w(qs.db.Where("event_name >= ?", eventName))
}

// EventNameIn is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventNameIn(eventName ...string) EventQuerySet {
	if len(eventName) == 0 {
		qs.db.AddError(errors.New("must at least pass one eventName in EventNameIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("event_name IN (?)", eventName))
}

// EventNameLike is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventNameLike(eventName string) EventQuerySet {
	return qs.w(qs.db.Where("event_name LIKE ?", eventName))
}

// EventNameLt is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventNameLt(eventName string) EventQuerySet {
	return qs.w(qs.db.Where("event_name < ?", eventName))
}

// EventNameLte is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventNameLte(eventName string) EventQuerySet {
	return qs.w(qs.db.Where("event_name <= ?", eventName))
}

// EventNameNe is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventNameNe(eventName string) EventQuerySet {
	return qs.w(qs.db.Where("event_name != ?", eventName))
}

// EventNameNotIn is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventNameNotIn(eventName ...string) EventQuerySet {
	if len(eventName) == 0 {
		qs.db.AddError(errors.New("must at least pass one eventName in EventNameNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("event_name NOT IN (?)", eventName))
}

// EventNameNotlike is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) EventNameNotlike(eventName string) EventQuerySet {
	return qs.w(qs.db.Where("event_name NOT LIKE ?", eventName))
}

// GetDB is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) GetDB() *gorm.DB {
	return qs.db
}

// GetUpdater is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) GetUpdater() EventUpdater {
	return NewEventUpdater(qs.db)
}

// LastModifyTimeEq is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) LastModifyTimeEq(lastModifyTime time.Time) EventQuerySet {
	return qs.w(qs.db.Where("last_modify_time = ?", lastModifyTime))
}

// LastModifyTimeGt is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) LastModifyTimeGt(lastModifyTime time.Time) EventQuerySet {
	return qs.w(qs.db.Where("last_modify_time > ?", lastModifyTime))
}

// LastModifyTimeGte is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) LastModifyTimeGte(lastModifyTime time.Time) EventQuerySet {
	return qs.w(qs.db.Where("last_modify_time >= ?", lastModifyTime))
}

// LastModifyTimeLt is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) LastModifyTimeLt(lastModifyTime time.Time) EventQuerySet {
	return qs.w(qs.db.Where("last_modify_time < ?", lastModifyTime))
}

// LastModifyTimeLte is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) LastModifyTimeLte(lastModifyTime time.Time) EventQuerySet {
	return qs.w(qs.db.Where("last_modify_time <= ?", lastModifyTime))
}

// LastModifyTimeNe is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) LastModifyTimeNe(lastModifyTime time.Time) EventQuerySet {
	return qs.w(qs.db.Where("last_modify_time != ?", lastModifyTime))
}

// Limit is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) Limit(limit int) EventQuerySet {
	return qs.w(qs.db.Limit(limit))
}

// Offset is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) Offset(offset int) EventQuerySet {
	return qs.w(qs.db.Offset(offset))
}

// One is used to retrieve one result. It returns gorm.ErrRecordNotFound
// if nothing was fetched
func (qs EventQuerySet) One(ret *Event) error {
	return qs.db.First(ret).Error
}

// OrderAscByDimensionList is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) OrderAscByDimensionList() EventQuerySet {
	return qs.w(qs.db.Order("dimension_list ASC"))
}

// OrderAscByEventGroupID is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) OrderAscByEventGroupID() EventQuerySet {
	return qs.w(qs.db.Order("event_group_id ASC"))
}

// OrderAscByEventID is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) OrderAscByEventID() EventQuerySet {
	return qs.w(qs.db.Order("event_id ASC"))
}

// OrderAscByEventName is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) OrderAscByEventName() EventQuerySet {
	return qs.w(qs.db.Order("event_name ASC"))
}

// OrderAscByLastModifyTime is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) OrderAscByLastModifyTime() EventQuerySet {
	return qs.w(qs.db.Order("last_modify_time ASC"))
}

// OrderDescByDimensionList is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) OrderDescByDimensionList() EventQuerySet {
	return qs.w(qs.db.Order("dimension_list DESC"))
}

// OrderDescByEventGroupID is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) OrderDescByEventGroupID() EventQuerySet {
	return qs.w(qs.db.Order("event_group_id DESC"))
}

// OrderDescByEventID is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) OrderDescByEventID() EventQuerySet {
	return qs.w(qs.db.Order("event_id DESC"))
}

// OrderDescByEventName is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) OrderDescByEventName() EventQuerySet {
	return qs.w(qs.db.Order("event_name DESC"))
}

// OrderDescByLastModifyTime is an autogenerated method
// nolint: dupl
func (qs EventQuerySet) OrderDescByLastModifyTime() EventQuerySet {
	return qs.w(qs.db.Order("last_modify_time DESC"))
}

// SetDimensionList is an autogenerated method
// nolint: dupl
func (u EventUpdater) SetDimensionList(dimensionList string) EventUpdater {
	u.fields[string(EventDBSchema.DimensionList)] = dimensionList
	return u
}

// SetEventGroupID is an autogenerated method
// nolint: dupl
func (u EventUpdater) SetEventGroupID(eventGroupID uint) EventUpdater {
	u.fields[string(EventDBSchema.EventGroupID)] = eventGroupID
	return u
}

// SetEventID is an autogenerated method
// nolint: dupl
func (u EventUpdater) SetEventID(eventID uint) EventUpdater {
	u.fields[string(EventDBSchema.EventID)] = eventID
	return u
}

// SetEventName is an autogenerated method
// nolint: dupl
func (u EventUpdater) SetEventName(eventName string) EventUpdater {
	u.fields[string(EventDBSchema.EventName)] = eventName
	return u
}

// SetLastModifyTime is an autogenerated method
// nolint: dupl
func (u EventUpdater) SetLastModifyTime(lastModifyTime time.Time) EventUpdater {
	u.fields[string(EventDBSchema.LastModifyTime)] = lastModifyTime
	return u
}

// Update is an autogenerated method
// nolint: dupl
func (u EventUpdater) Update() error {
	return u.db.Updates(u.fields).Error
}

// UpdateNum is an autogenerated method
// nolint: dupl
func (u EventUpdater) UpdateNum() (int64, error) {
	db := u.db.Updates(u.fields)
	return db.RowsAffected, db.Error
}

// ===== END of query set EventQuerySet

// ===== BEGIN of Event modifiers

// EventDBSchemaField describes database schema field. It requires for method 'Update'
type EventDBSchemaField string

// String method returns string representation of field.
// nolint: dupl
func (f EventDBSchemaField) String() string {
	return string(f)
}

// EventDBSchema stores db field names of Event
var EventDBSchema = struct {
	EventID        EventDBSchemaField
	EventGroupID   EventDBSchemaField
	EventName      EventDBSchemaField
	DimensionList  EventDBSchemaField
	LastModifyTime EventDBSchemaField
}{

	EventID:        EventDBSchemaField("event_id"),
	EventGroupID:   EventDBSchemaField("event_group_id"),
	EventName:      EventDBSchemaField("event_name"),
	DimensionList:  EventDBSchemaField("dimension_list"),
	LastModifyTime: EventDBSchemaField("last_modify_time"),
}

// Update updates Event fields by primary key
// nolint: dupl
func (o *Event) Update(db *gorm.DB, fields ...EventDBSchemaField) error {
	dbNameToFieldName := map[string]interface{}{
		"event_id":         o.EventID,
		"event_group_id":   o.EventGroupID,
		"event_name":       o.EventName,
		"dimension_list":   o.DimensionList,
		"last_modify_time": o.LastModifyTime,
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

		return fmt.Errorf("can't update Event %v fields %v: %s",
			o, fields, err)
	}

	return nil
}

// EventUpdater is an Event updates manager
type EventUpdater struct {
	fields map[string]interface{}
	db     *gorm.DB
}

// NewEventUpdater creates new Event updater
// nolint: dupl
func NewEventUpdater(db *gorm.DB) EventUpdater {
	return EventUpdater{
		fields: map[string]interface{}{},
		db:     db.Model(&Event{}),
	}
}

// ===== END of Event modifiers

// ===== END of all query sets
