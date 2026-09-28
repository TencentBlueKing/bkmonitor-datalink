package models

type AlarmSource struct {
	Id            string             `json:"id"`
	Name          string             `json:"name"`
	LinkdSourceId string             `json:"linkd_source_id"`
	LinkdChannel  LinkdChannelConfig `json:"linkd_channel"`
}

type LinkdChannelConfig struct {
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}
