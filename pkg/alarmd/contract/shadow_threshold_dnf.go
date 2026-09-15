package contract

import "encoding/json"

// ThresholdDNFConfigV2 preserves the existing Threshold ANY-of-ALL predicate.
// Ordering is retained; this representation performs no Boolean rewriting.
type ThresholdDNFConfigV2 struct {
	Groups []ThresholdDNFGroupV2 `json:"groups"`
}
type ThresholdDNFGroupV2 struct {
	Conditions []ThresholdDNFConditionV2 `json:"conditions"`
}
type ThresholdDNFConditionV2 struct {
	Operator  string `json:"operator"`
	Threshold string `json:"threshold"`
}

func normalizeThresholdDNFV2(raw json.RawMessage) (json.RawMessage, error) {
	var c ThresholdDNFConfigV2
	if err := decodeShadowJSON(raw, len(raw), &c); err != nil {
		return nil, err
	}
	if len(c.Groups) == 0 {
		return nil, invalid("shadow.config.threshold", "empty DNF")
	}
	for i := range c.Groups {
		if len(c.Groups[i].Conditions) == 0 {
			return nil, invalid("shadow.config.threshold", "empty conjunction")
		}
		for j := range c.Groups[i].Conditions {
			p := &c.Groups[i].Conditions[j]
			if p.Operator != "GTE" && p.Operator != "GT" && p.Operator != "LTE" && p.Operator != "LT" && p.Operator != "EQ" && p.Operator != "NE" {
				return nil, invalid("shadow.config.threshold", "operator required")
			}
			var err error
			p.Threshold, err = NormalizeShadowDecimalV1(p.Threshold)
			if err != nil {
				return nil, err
			}
		}
	}
	return json.Marshal(c)
}
