package storage

//go:generate goqueryset -in surrealdbstorage.go -out qs_surrealdbstorage_gen.go

// SurrealDBStorage SurrealDB 图存储表。
// gen:qs
type SurrealDBStorage struct {
	TableID          string `json:"table_id" gorm:"primary_key;size:128"`
	BkTenantID       string `json:"bk_tenant_id" gorm:"primary_key;size:256;default:system"`
	StorageClusterID uint   `json:"storage_cluster_id" gorm:"comment:存储集群"`
}

func (SurrealDBStorage) TableName() string {
	return "metadata_surrealdbstorage"
}
