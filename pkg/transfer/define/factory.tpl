package define

import (
	"errors"
	"fmt"

	"github.com/cheekybits/genny/generic"
)

// FT : factory type
type FT generic.Type

// FTCreator : function to create FT
type FTCreator func(name string) (FT, error)

// mapFT : FT factory mappings
var mapFT = make(map[string]FTCreator)

// RegisterFT : register FT to factory
var RegisterFT = func (name string, fn FTCreator) {
	mapFT[name] = fn
}

// NewFT : create FT by name
var NewFT = func (name string) (FT, error) {
	fn, ok := mapFT[name]
	if !ok {
		return nil, fmt.Errorf("unknown FT %s", name)
	}
	return fn(name)
}

func init() {
	RegisterPlugin(&PluginInfo{
		Name: "FT",
		Registered: func() []string {
			keys := make([]string, 0, len(mapFT))
			for key := range mapFT {
				keys = append(keys, key)
			}
			return keys
		},
	})
}