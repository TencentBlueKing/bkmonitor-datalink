package progress

import (
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

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
