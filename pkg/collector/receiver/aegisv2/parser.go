package aegisv2

import (
	"encoding/json"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func parseCollectPayload(buf []byte) (collectPayload, error) {
	var payload collectPayload
	if err := json.Unmarshal(buf, &payload); err != nil {
		preview := getPayloadPreview(buf, 50)
		logger.Debugf("aegisv2 parseCollectPayload failed to unmarshal, payload preview: %s, error: %v", preview, err)
		return collectPayload{}, ErrNotAegisV2
	}
	if payload.Topic == "" && payload.Scheme == "" && len(payload.D2) == 0 {
		return collectPayload{}, ErrNotAegisV2
	}
	return payload, nil
}

func parseD2Records(raw json.RawMessage) ([]d2Record, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var records []d2Record
	return records, json.Unmarshal(raw, &records)
}

func getPayloadPreview(buf []byte, maxLen int) string {
	if len(buf) == 0 {
		return "<empty>"
	}
	if len(buf) <= maxLen {
		return escapeString(string(buf))
	}
	return escapeString(string(buf[:maxLen])) + "..."
}

func escapeString(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 32 && c < 127 && c != '\\' && c != '"' {
			b = append(b, c)
		} else {
			switch c {
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default:
				b = append(b, '?')
			}
		}
	}
	return string(b)
}
