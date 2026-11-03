package linkdoutput

// ActionClosed is the action of a level written to end its alert closed
// (status_setter close; LevelAction).
const ActionClosed = "closed"

// The changes an edit close names as the reason an alert of its strategy
// was orphaned (package editclose).
const (
	StrategyChangedMetric     = "metric_changed"
	StrategyChangedCondition  = "condition_changed"
	StrategyChangedDimensions = "dimensions_changed"
	StrategyChangedLevel      = "level_removed"
	StrategyChangedNoData     = "no_data_disabled"
)
