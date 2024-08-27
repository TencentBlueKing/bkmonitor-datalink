package slo

import (
	"time"
)

const TableNameAlarmStrategyV2 = "alarm_strategy_v2"

//go:generate goqueryset -in alarm_strategy_v2.go -out qs_alarm_strategy_v2_gen.go

// AlarmStrategyV2 mapped from table <alarm_strategy_v2>
type AlarmStrategyV2 struct {
	ID               int32     `gorm:"column:id;primaryKey;autoIncrement:true" json:"id"`
	Name             string    `gorm:"column:name;not null" json:"name"`
	BkBizID          int32     `gorm:"column:bk_biz_id;not null" json:"bk_biz_id"`
	Source           string    `gorm:"column:source;not null" json:"source"`
	Scenario         string    `gorm:"column:scenario;not null" json:"scenario"`
	Type             string    `gorm:"column:type;not null" json:"type"`
	IsEnabled        bool      `gorm:"column:is_enabled;not null" json:"is_enabled"`
	CreateUser       string    `gorm:"column:create_user;not null" json:"create_user"`
	CreateTime       time.Time `gorm:"column:create_time;not null" json:"create_time"`
	UpdateUser       string    `gorm:"column:update_user;not null" json:"update_user"`
	UpdateTime       time.Time `gorm:"column:update_time;not null" json:"update_time"`
	IsInvalid        bool      `gorm:"column:is_invalid;not null" json:"is_invalid"`
	InvalidType      string    `gorm:"column:invalid_type;not null" json:"invalid_type"`
	App              string    `gorm:"column:app" json:"app"`
	Hash             string    `gorm:"column:hash" json:"hash"`
	Path             string    `gorm:"column:path" json:"path"`
	Snippet          string    `gorm:"column:snippet" json:"snippet"`
	Priority         int32     `gorm:"column:priority" json:"priority"`
	PriorityGroupKey string    `gorm:"column:priority_group_key" json:"priority_group_key"`
}

// TableName AlarmStrategyV2's table name
func (*AlarmStrategyV2) TableName() string {
	return TableNameAlarmStrategyV2
}
