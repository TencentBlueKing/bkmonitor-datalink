package slo

const TableNameAlarmQueryConfigV2 = "alarm_query_config_v2"

// AlarmQueryConfigV2 mapped from table <alarm_query_config_v2>
type AlarmQueryConfigV2 struct {
	ID              int32  `gorm:"column:id;primaryKey;autoIncrement:true" json:"id"`
	StrategyID      int32  `gorm:"column:strategy_id;not null" json:"strategy_id"`
	ItemID          int32  `gorm:"column:item_id;not null" json:"item_id"`
	Alias           string `gorm:"column:alias;not null" json:"alias"`
	DataSourceLabel string `gorm:"column:data_source_label;not null" json:"data_source_label"`
	DataTypeLabel   string `gorm:"column:data_type_label;not null" json:"data_type_label"`
	MetricID        string `gorm:"column:metric_id;not null" json:"metric_id"`
	Config          string `gorm:"column:config;not null" json:"config"`
}

// TableName AlarmQueryConfigV2's table name
func (*AlarmQueryConfigV2) TableName() string {
	return TableNameAlarmQueryConfigV2
}
