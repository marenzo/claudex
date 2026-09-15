package quota

import "encoding/json"

// CodexUsageAllowsRecovery requires explicit availability for the base allowance
// and every additional inference pool before lifting a credential-wide cooldown.
// Code-review allowance is independent of model inference and is not consulted.
func CodexUsageAllowsRecovery(body []byte) bool {
	type allowance struct {
		Allowed      *bool `json:"allowed"`
		LimitReached *bool `json:"limit_reached"`
	}
	var usage struct {
		RateLimit  *allowance `json:"rate_limit"`
		Additional []struct {
			RateLimit *allowance `json:"rate_limit"`
		} `json:"additional_rate_limits"`
		ReachedType *string         `json:"rate_limit_reached_type"`
		Error       json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &usage) != nil || (len(usage.Error) > 0 && string(usage.Error) != "null") ||
		(usage.ReachedType != nil && *usage.ReachedType != "") {
		return false
	}
	allowed := func(limit *allowance) bool {
		return limit != nil && limit.Allowed != nil && *limit.Allowed &&
			limit.LimitReached != nil && !*limit.LimitReached
	}
	if !allowed(usage.RateLimit) {
		return false
	}
	for _, extra := range usage.Additional {
		if !allowed(extra.RateLimit) {
			return false
		}
	}
	return true
}
