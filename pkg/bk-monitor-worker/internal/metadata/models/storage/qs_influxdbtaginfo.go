// Code generated by go-queryset. DO NOT EDIT.
package storage

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jinzhu/gorm"
)

// ===== BEGIN of all query sets

// ===== BEGIN of query set InfluxdbTagInfoQuerySet

// InfluxdbTagInfoQuerySet is an queryset type for InfluxdbTagInfo
type InfluxdbTagInfoQuerySet struct {
	db *gorm.DB
}

// NewInfluxdbTagInfoQuerySet constructs new InfluxdbTagInfoQuerySet
func NewInfluxdbTagInfoQuerySet(db *gorm.DB) InfluxdbTagInfoQuerySet {
	return InfluxdbTagInfoQuerySet{
		db: db.Model(&InfluxdbTagInfo{}),
	}
}

func (qs InfluxdbTagInfoQuerySet) w(db *gorm.DB) InfluxdbTagInfoQuerySet {
	return NewInfluxdbTagInfoQuerySet(db)
}

func (qs InfluxdbTagInfoQuerySet) Select(fields ...InfluxdbTagInfoDBSchemaField) InfluxdbTagInfoQuerySet {
	names := []string{}
	for _, f := range fields {
		names = append(names, f.String())
	}

	return qs.w(qs.db.Select(strings.Join(names, ",")))
}

// Create is an autogenerated method
// nolint: dupl
func (o *InfluxdbTagInfo) Create(db *gorm.DB) error {
	return db.Create(o).Error
}

// Delete is an autogenerated method
// nolint: dupl
func (o *InfluxdbTagInfo) Delete(db *gorm.DB) error {
	return db.Delete(o).Error
}

// All is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) All(ret *[]InfluxdbTagInfo) error {
	return qs.db.Find(ret).Error
}

// ClusterNameEq is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ClusterNameEq(clusterName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("cluster_name = ?", clusterName))
}

// ClusterNameGt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ClusterNameGt(clusterName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("cluster_name > ?", clusterName))
}

// ClusterNameGte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ClusterNameGte(clusterName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("cluster_name >= ?", clusterName))
}

// ClusterNameIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ClusterNameIn(clusterName ...string) InfluxdbTagInfoQuerySet {
	if len(clusterName) == 0 {
		qs.db.AddError(errors.New("must at least pass one clusterName in ClusterNameIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("cluster_name IN (?)", clusterName))
}

// ClusterNameLike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ClusterNameLike(clusterName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("cluster_name LIKE ?", clusterName))
}

// ClusterNameLt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ClusterNameLt(clusterName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("cluster_name < ?", clusterName))
}

// ClusterNameLte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ClusterNameLte(clusterName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("cluster_name <= ?", clusterName))
}

// ClusterNameNe is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ClusterNameNe(clusterName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("cluster_name != ?", clusterName))
}

// ClusterNameNotIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ClusterNameNotIn(clusterName ...string) InfluxdbTagInfoQuerySet {
	if len(clusterName) == 0 {
		qs.db.AddError(errors.New("must at least pass one clusterName in ClusterNameNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("cluster_name NOT IN (?)", clusterName))
}

// ClusterNameNotlike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ClusterNameNotlike(clusterName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("cluster_name NOT LIKE ?", clusterName))
}

// Count is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) Count() (int, error) {
	var count int
	err := qs.db.Count(&count).Error
	return count, err
}

// DatabaseEq is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) DatabaseEq(database string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("database = ?", database))
}

// DatabaseGt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) DatabaseGt(database string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("database > ?", database))
}

// DatabaseGte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) DatabaseGte(database string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("database >= ?", database))
}

// DatabaseIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) DatabaseIn(database ...string) InfluxdbTagInfoQuerySet {
	if len(database) == 0 {
		qs.db.AddError(errors.New("must at least pass one database in DatabaseIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("database IN (?)", database))
}

// DatabaseLike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) DatabaseLike(database string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("database LIKE ?", database))
}

// DatabaseLt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) DatabaseLt(database string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("database < ?", database))
}

// DatabaseLte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) DatabaseLte(database string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("database <= ?", database))
}

// DatabaseNe is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) DatabaseNe(database string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("database != ?", database))
}

// DatabaseNotIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) DatabaseNotIn(database ...string) InfluxdbTagInfoQuerySet {
	if len(database) == 0 {
		qs.db.AddError(errors.New("must at least pass one database in DatabaseNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("database NOT IN (?)", database))
}

// DatabaseNotlike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) DatabaseNotlike(database string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("database NOT LIKE ?", database))
}

// Delete is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) Delete() error {
	return qs.db.Delete(InfluxdbTagInfo{}).Error
}

// DeleteNum is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) DeleteNum() (int64, error) {
	db := qs.db.Delete(InfluxdbTagInfo{})
	return db.RowsAffected, db.Error
}

// DeleteNumUnscoped is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) DeleteNumUnscoped() (int64, error) {
	db := qs.db.Unscoped().Delete(InfluxdbTagInfo{})
	return db.RowsAffected, db.Error
}

// ForceOverwriteEq is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ForceOverwriteEq(forceOverwrite bool) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("force_overwrite = ?", forceOverwrite))
}

// ForceOverwriteIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ForceOverwriteIn(forceOverwrite ...bool) InfluxdbTagInfoQuerySet {
	if len(forceOverwrite) == 0 {
		qs.db.AddError(errors.New("must at least pass one forceOverwrite in ForceOverwriteIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("force_overwrite IN (?)", forceOverwrite))
}

// ForceOverwriteNe is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ForceOverwriteNe(forceOverwrite bool) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("force_overwrite != ?", forceOverwrite))
}

// ForceOverwriteNotIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ForceOverwriteNotIn(forceOverwrite ...bool) InfluxdbTagInfoQuerySet {
	if len(forceOverwrite) == 0 {
		qs.db.AddError(errors.New("must at least pass one forceOverwrite in ForceOverwriteNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("force_overwrite NOT IN (?)", forceOverwrite))
}

// GetDB is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) GetDB() *gorm.DB {
	return qs.db
}

// GetUpdater is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) GetUpdater() InfluxdbTagInfoUpdater {
	return NewInfluxdbTagInfoUpdater(qs.db)
}

// HostListEq is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) HostListEq(hostList string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("host_list = ?", hostList))
}

// HostListGt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) HostListGt(hostList string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("host_list > ?", hostList))
}

// HostListGte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) HostListGte(hostList string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("host_list >= ?", hostList))
}

// HostListIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) HostListIn(hostList ...string) InfluxdbTagInfoQuerySet {
	if len(hostList) == 0 {
		qs.db.AddError(errors.New("must at least pass one hostList in HostListIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("host_list IN (?)", hostList))
}

// HostListLike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) HostListLike(hostList string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("host_list LIKE ?", hostList))
}

// HostListLt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) HostListLt(hostList string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("host_list < ?", hostList))
}

// HostListLte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) HostListLte(hostList string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("host_list <= ?", hostList))
}

// HostListNe is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) HostListNe(hostList string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("host_list != ?", hostList))
}

// HostListNotIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) HostListNotIn(hostList ...string) InfluxdbTagInfoQuerySet {
	if len(hostList) == 0 {
		qs.db.AddError(errors.New("must at least pass one hostList in HostListNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("host_list NOT IN (?)", hostList))
}

// HostListNotlike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) HostListNotlike(hostList string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("host_list NOT LIKE ?", hostList))
}

// Limit is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) Limit(limit int) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Limit(limit))
}

// ManualUnreadableHostEq is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ManualUnreadableHostEq(manualUnreadableHost string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("manual_unreadable_host = ?", manualUnreadableHost))
}

// ManualUnreadableHostGt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ManualUnreadableHostGt(manualUnreadableHost string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("manual_unreadable_host > ?", manualUnreadableHost))
}

// ManualUnreadableHostGte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ManualUnreadableHostGte(manualUnreadableHost string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("manual_unreadable_host >= ?", manualUnreadableHost))
}

// ManualUnreadableHostIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ManualUnreadableHostIn(manualUnreadableHost ...string) InfluxdbTagInfoQuerySet {
	if len(manualUnreadableHost) == 0 {
		qs.db.AddError(errors.New("must at least pass one manualUnreadableHost in ManualUnreadableHostIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("manual_unreadable_host IN (?)", manualUnreadableHost))
}

// ManualUnreadableHostLike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ManualUnreadableHostLike(manualUnreadableHost string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("manual_unreadable_host LIKE ?", manualUnreadableHost))
}

// ManualUnreadableHostLt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ManualUnreadableHostLt(manualUnreadableHost string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("manual_unreadable_host < ?", manualUnreadableHost))
}

// ManualUnreadableHostLte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ManualUnreadableHostLte(manualUnreadableHost string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("manual_unreadable_host <= ?", manualUnreadableHost))
}

// ManualUnreadableHostNe is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ManualUnreadableHostNe(manualUnreadableHost string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("manual_unreadable_host != ?", manualUnreadableHost))
}

// ManualUnreadableHostNotIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ManualUnreadableHostNotIn(manualUnreadableHost ...string) InfluxdbTagInfoQuerySet {
	if len(manualUnreadableHost) == 0 {
		qs.db.AddError(errors.New("must at least pass one manualUnreadableHost in ManualUnreadableHostNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("manual_unreadable_host NOT IN (?)", manualUnreadableHost))
}

// ManualUnreadableHostNotlike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) ManualUnreadableHostNotlike(manualUnreadableHost string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("manual_unreadable_host NOT LIKE ?", manualUnreadableHost))
}

// MeasurementEq is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) MeasurementEq(measurement string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("measurement = ?", measurement))
}

// MeasurementGt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) MeasurementGt(measurement string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("measurement > ?", measurement))
}

// MeasurementGte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) MeasurementGte(measurement string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("measurement >= ?", measurement))
}

// MeasurementIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) MeasurementIn(measurement ...string) InfluxdbTagInfoQuerySet {
	if len(measurement) == 0 {
		qs.db.AddError(errors.New("must at least pass one measurement in MeasurementIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("measurement IN (?)", measurement))
}

// MeasurementLike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) MeasurementLike(measurement string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("measurement LIKE ?", measurement))
}

// MeasurementLt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) MeasurementLt(measurement string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("measurement < ?", measurement))
}

// MeasurementLte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) MeasurementLte(measurement string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("measurement <= ?", measurement))
}

// MeasurementNe is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) MeasurementNe(measurement string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("measurement != ?", measurement))
}

// MeasurementNotIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) MeasurementNotIn(measurement ...string) InfluxdbTagInfoQuerySet {
	if len(measurement) == 0 {
		qs.db.AddError(errors.New("must at least pass one measurement in MeasurementNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("measurement NOT IN (?)", measurement))
}

// MeasurementNotlike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) MeasurementNotlike(measurement string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("measurement NOT LIKE ?", measurement))
}

// Offset is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) Offset(offset int) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Offset(offset))
}

// One is used to retrieve one result. It returns gorm.ErrRecordNotFound
// if nothing was fetched
func (qs InfluxdbTagInfoQuerySet) One(ret *InfluxdbTagInfo) error {
	return qs.db.First(ret).Error
}

// OrderAscByClusterName is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderAscByClusterName() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("cluster_name ASC"))
}

// OrderAscByDatabase is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderAscByDatabase() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("database ASC"))
}

// OrderAscByForceOverwrite is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderAscByForceOverwrite() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("force_overwrite ASC"))
}

// OrderAscByHostList is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderAscByHostList() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("host_list ASC"))
}

// OrderAscByManualUnreadableHost is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderAscByManualUnreadableHost() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("manual_unreadable_host ASC"))
}

// OrderAscByMeasurement is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderAscByMeasurement() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("measurement ASC"))
}

// OrderAscByTagName is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderAscByTagName() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("tag_name ASC"))
}

// OrderAscByTagValue is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderAscByTagValue() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("tag_value ASC"))
}

// OrderDescByClusterName is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderDescByClusterName() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("cluster_name DESC"))
}

// OrderDescByDatabase is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderDescByDatabase() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("database DESC"))
}

// OrderDescByForceOverwrite is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderDescByForceOverwrite() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("force_overwrite DESC"))
}

// OrderDescByHostList is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderDescByHostList() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("host_list DESC"))
}

// OrderDescByManualUnreadableHost is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderDescByManualUnreadableHost() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("manual_unreadable_host DESC"))
}

// OrderDescByMeasurement is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderDescByMeasurement() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("measurement DESC"))
}

// OrderDescByTagName is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderDescByTagName() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("tag_name DESC"))
}

// OrderDescByTagValue is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) OrderDescByTagValue() InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Order("tag_value DESC"))
}

// TagNameEq is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagNameEq(tagName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_name = ?", tagName))
}

// TagNameGt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagNameGt(tagName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_name > ?", tagName))
}

// TagNameGte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagNameGte(tagName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_name >= ?", tagName))
}

// TagNameIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagNameIn(tagName ...string) InfluxdbTagInfoQuerySet {
	if len(tagName) == 0 {
		qs.db.AddError(errors.New("must at least pass one tagName in TagNameIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("tag_name IN (?)", tagName))
}

// TagNameLike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagNameLike(tagName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_name LIKE ?", tagName))
}

// TagNameLt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagNameLt(tagName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_name < ?", tagName))
}

// TagNameLte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagNameLte(tagName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_name <= ?", tagName))
}

// TagNameNe is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagNameNe(tagName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_name != ?", tagName))
}

// TagNameNotIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagNameNotIn(tagName ...string) InfluxdbTagInfoQuerySet {
	if len(tagName) == 0 {
		qs.db.AddError(errors.New("must at least pass one tagName in TagNameNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("tag_name NOT IN (?)", tagName))
}

// TagNameNotlike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagNameNotlike(tagName string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_name NOT LIKE ?", tagName))
}

// TagValueEq is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagValueEq(tagValue string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_value = ?", tagValue))
}

// TagValueGt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagValueGt(tagValue string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_value > ?", tagValue))
}

// TagValueGte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagValueGte(tagValue string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_value >= ?", tagValue))
}

// TagValueIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagValueIn(tagValue ...string) InfluxdbTagInfoQuerySet {
	if len(tagValue) == 0 {
		qs.db.AddError(errors.New("must at least pass one tagValue in TagValueIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("tag_value IN (?)", tagValue))
}

// TagValueLike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagValueLike(tagValue string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_value LIKE ?", tagValue))
}

// TagValueLt is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagValueLt(tagValue string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_value < ?", tagValue))
}

// TagValueLte is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagValueLte(tagValue string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_value <= ?", tagValue))
}

// TagValueNe is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagValueNe(tagValue string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_value != ?", tagValue))
}

// TagValueNotIn is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagValueNotIn(tagValue ...string) InfluxdbTagInfoQuerySet {
	if len(tagValue) == 0 {
		qs.db.AddError(errors.New("must at least pass one tagValue in TagValueNotIn"))
		return qs.w(qs.db)
	}
	return qs.w(qs.db.Where("tag_value NOT IN (?)", tagValue))
}

// TagValueNotlike is an autogenerated method
// nolint: dupl
func (qs InfluxdbTagInfoQuerySet) TagValueNotlike(tagValue string) InfluxdbTagInfoQuerySet {
	return qs.w(qs.db.Where("tag_value NOT LIKE ?", tagValue))
}

// SetClusterName is an autogenerated method
// nolint: dupl
func (u InfluxdbTagInfoUpdater) SetClusterName(clusterName string) InfluxdbTagInfoUpdater {
	u.fields[string(InfluxdbTagInfoDBSchema.ClusterName)] = clusterName
	return u
}

// SetDatabase is an autogenerated method
// nolint: dupl
func (u InfluxdbTagInfoUpdater) SetDatabase(database string) InfluxdbTagInfoUpdater {
	u.fields[string(InfluxdbTagInfoDBSchema.Database)] = database
	return u
}

// SetForceOverwrite is an autogenerated method
// nolint: dupl
func (u InfluxdbTagInfoUpdater) SetForceOverwrite(forceOverwrite bool) InfluxdbTagInfoUpdater {
	u.fields[string(InfluxdbTagInfoDBSchema.ForceOverwrite)] = forceOverwrite
	return u
}

// SetHostList is an autogenerated method
// nolint: dupl
func (u InfluxdbTagInfoUpdater) SetHostList(hostList string) InfluxdbTagInfoUpdater {
	u.fields[string(InfluxdbTagInfoDBSchema.HostList)] = hostList
	return u
}

// SetManualUnreadableHost is an autogenerated method
// nolint: dupl
func (u InfluxdbTagInfoUpdater) SetManualUnreadableHost(manualUnreadableHost string) InfluxdbTagInfoUpdater {
	u.fields[string(InfluxdbTagInfoDBSchema.ManualUnreadableHost)] = manualUnreadableHost
	return u
}

// SetMeasurement is an autogenerated method
// nolint: dupl
func (u InfluxdbTagInfoUpdater) SetMeasurement(measurement string) InfluxdbTagInfoUpdater {
	u.fields[string(InfluxdbTagInfoDBSchema.Measurement)] = measurement
	return u
}

// SetTagName is an autogenerated method
// nolint: dupl
func (u InfluxdbTagInfoUpdater) SetTagName(tagName string) InfluxdbTagInfoUpdater {
	u.fields[string(InfluxdbTagInfoDBSchema.TagName)] = tagName
	return u
}

// SetTagValue is an autogenerated method
// nolint: dupl
func (u InfluxdbTagInfoUpdater) SetTagValue(tagValue string) InfluxdbTagInfoUpdater {
	u.fields[string(InfluxdbTagInfoDBSchema.TagValue)] = tagValue
	return u
}

// Update is an autogenerated method
// nolint: dupl
func (u InfluxdbTagInfoUpdater) Update() error {
	return u.db.Updates(u.fields).Error
}

// UpdateNum is an autogenerated method
// nolint: dupl
func (u InfluxdbTagInfoUpdater) UpdateNum() (int64, error) {
	db := u.db.Updates(u.fields)
	return db.RowsAffected, db.Error
}

// ===== END of query set InfluxdbTagInfoQuerySet

// ===== BEGIN of InfluxdbTagInfo modifiers

// InfluxdbTagInfoDBSchemaField describes database schema field. It requires for method 'Update'
type InfluxdbTagInfoDBSchemaField string

// String method returns string representation of field.
// nolint: dupl
func (f InfluxdbTagInfoDBSchemaField) String() string {
	return string(f)
}

// InfluxdbTagInfoDBSchema stores db field names of InfluxdbTagInfo
var InfluxdbTagInfoDBSchema = struct {
	Database             InfluxdbTagInfoDBSchemaField
	Measurement          InfluxdbTagInfoDBSchemaField
	TagName              InfluxdbTagInfoDBSchemaField
	TagValue             InfluxdbTagInfoDBSchemaField
	ClusterName          InfluxdbTagInfoDBSchemaField
	HostList             InfluxdbTagInfoDBSchemaField
	ManualUnreadableHost InfluxdbTagInfoDBSchemaField
	ForceOverwrite       InfluxdbTagInfoDBSchemaField
}{

	Database:             InfluxdbTagInfoDBSchemaField("database"),
	Measurement:          InfluxdbTagInfoDBSchemaField("measurement"),
	TagName:              InfluxdbTagInfoDBSchemaField("tag_name"),
	TagValue:             InfluxdbTagInfoDBSchemaField("tag_value"),
	ClusterName:          InfluxdbTagInfoDBSchemaField("cluster_name"),
	HostList:             InfluxdbTagInfoDBSchemaField("host_list"),
	ManualUnreadableHost: InfluxdbTagInfoDBSchemaField("manual_unreadable_host"),
	ForceOverwrite:       InfluxdbTagInfoDBSchemaField("force_overwrite"),
}

// Update updates InfluxdbTagInfo fields by primary key
// nolint: dupl
func (o *InfluxdbTagInfo) Update(db *gorm.DB, fields ...InfluxdbTagInfoDBSchemaField) error {
	dbNameToFieldName := map[string]interface{}{
		"database":               o.Database,
		"measurement":            o.Measurement,
		"tag_name":               o.TagName,
		"tag_value":              o.TagValue,
		"cluster_name":           o.ClusterName,
		"host_list":              o.HostList,
		"manual_unreadable_host": o.ManualUnreadableHost,
		"force_overwrite":        o.ForceOverwrite,
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

		return fmt.Errorf("can't update InfluxdbTagInfo %v fields %v: %s",
			o, fields, err)
	}

	return nil
}

// InfluxdbTagInfoUpdater is an InfluxdbTagInfo updates manager
type InfluxdbTagInfoUpdater struct {
	fields map[string]interface{}
	db     *gorm.DB
}

// NewInfluxdbTagInfoUpdater creates new InfluxdbTagInfo updater
// nolint: dupl
func NewInfluxdbTagInfoUpdater(db *gorm.DB) InfluxdbTagInfoUpdater {
	return InfluxdbTagInfoUpdater{
		fields: map[string]interface{}{},
		db:     db.Model(&InfluxdbTagInfo{}),
	}
}

// ===== END of InfluxdbTagInfo modifiers

// ===== END of all query sets
