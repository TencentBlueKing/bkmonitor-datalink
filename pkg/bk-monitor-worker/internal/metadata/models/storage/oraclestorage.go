package storage

type OracleStorage struct {
	TableID          string `json:"table_id" gorm:"primary_key;size:128"`
	StorageClusterID uint   `json:"storage_cluster_id" gorm:"storage_cluster_id"`
}

// TableName 用于设置表的别名
func (OracleStorage) TableName() string {
	return "metadata_oraclestorage"
}
