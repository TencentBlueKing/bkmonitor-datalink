package ownership

import (
	"errors"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ObservationControlKey lets a bounded read use the existing control key layout.
func (store *RedisStore) ObservationControlKey(group execution.QueryGroupIdentity, namespace string) (string, error) {
	if store == nil || group == "" || namespace == "" || strings.ContainsAny(namespace, "{} \t\r\n") {
		return "", errors.New("invalid control identity")
	}
	return store.controlKey(group, namespace), nil
}
