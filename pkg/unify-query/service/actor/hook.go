package actor

import (
	"context"
	"fmt"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// setDefaultConfig
func setDefaultConfig() {
	viper.SetDefault(MaxActorNumConfigPath, 10)
}

// LoadConfig
func LoadConfig() {
	MaxActorNum = viper.GetInt(MaxActorNumConfigPath)

	log.Debugf(context.TODO(),
		"reload success new config: MaxActorNum:%d",
		MaxActorNum,
	)
}

// init
func init() {
	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPreParse, setDefaultConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for http module for default config, maybe http module won't working.",
			eventbus.EventSignalConfigPreParse,
		)
	}

	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPostParse, LoadConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for http module for new config, maybe http module won't working.",
			eventbus.EventSignalConfigPostParse,
		)
	}
}
