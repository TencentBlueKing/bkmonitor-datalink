package storage

//go:generate goqueryset -in surrealdbbindingconfig.go -out qs_surrealdbbindingconfig_gen.go

// SurrealDBBindingConfig SurrealDB 图结果表绑定配置。
// gen:qs
type SurrealDBBindingConfig struct {
	ID                    uint   `json:"id" gorm:"primary_key"`
	BkTenantID            string `json:"bk_tenant_id" gorm:"size:256;default:system"`
	TableID               string `json:"table_id" gorm:"size:255"`
	Namespace             string `json:"namespace" gorm:"size:128"`
	BkbaseResultTableName string `json:"bkbase_result_table_name" gorm:"size:255"`
}

func (SurrealDBBindingConfig) TableName() string {
	return "metadata_surrealdbbindingconfig"
}
