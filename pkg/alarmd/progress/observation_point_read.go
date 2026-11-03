package progress

import (
	"errors"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ObservationControlKeys is the physical-key contract already implemented by
// the production ownership store. A diagnostic reader needs no slot resolver
// or write-capable Progress Store.
type ObservationControlKeys interface {
	ObservationControlKey(execution.QueryGroupIdentity, string) (string, error)
}

type ObservationKeys struct {
	prefix  string
	control ObservationControlKeys
}

func NewObservationKeys(prefix string, control ObservationControlKeys) (*ObservationKeys, error) {
	if prefix == "" || strings.ContainsAny(prefix, "{} \t\r\n") || control == nil {
		return nil, errors.New("progress: invalid observation key options")
	}
	return &ObservationKeys{prefix: prefix, control: control}, nil
}

func (keys *ObservationKeys) ObservationKey(group execution.QueryGroupIdentity) (string, error) {
	if keys == nil || group == "" {
		return "", errors.New("progress: complete observation identity is required")
	}
	return keys.control.ObservationControlKey(group, keys.prefix+":progress")
}

// ObservationKey delegates both namespace and physical key construction to the
// stores used by execution; it does not read or change progress.
func (store *Store) ObservationKey(group execution.QueryGroupIdentity) (string, error) {
	if store == nil {
		return "", errors.New("progress store unavailable")
	}
	namespace, err := store.namespace(execution.ProgressIdentity{QueryGroup: group})
	if err != nil {
		return "", err
	}
	builder, ok := store.options.Control.(interface {
		ObservationControlKey(execution.QueryGroupIdentity, string) (string, error)
	})
	if !ok {
		return "", errors.New("progress observation key unavailable")
	}
	return builder.ObservationControlKey(group, namespace)
}

// DecodeObserved is the production persisted-progress decoder and validator.
// Its caller bounds the bytes before decoding. No state is written.
func DecodeObserved(payload []byte) (execution.ScheduleProgress, error) { return decode(payload) }
