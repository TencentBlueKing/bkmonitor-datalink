package redis

// ScrollEndStrategy defines the strategy for ending scroll queries
type ScrollEndStrategy string

const (
	// EndOnEmptyData ends scroll when no data is returned (default strategy)
	EndOnEmptyData ScrollEndStrategy = "empty_data"
	// EndOnDataLimit ends scroll when specified data limit is reached
	EndOnDataLimit ScrollEndStrategy = "data_limit"
)
