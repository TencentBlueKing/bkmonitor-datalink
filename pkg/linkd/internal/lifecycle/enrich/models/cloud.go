package models

// CloudResource 是云平台资源在 Enrich 中的最小只读投影。
type CloudResource struct {
	TenantID        string
	CloudID         string
	InstanceID      string
	ResourceType    string
	Name            string
	CloudName       string
	ObjectModelCode string
	ModelInstanceID string
}
