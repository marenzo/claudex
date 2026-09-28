package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Revision fingerprints the settings a running gateway serves. It leaves out
// Dashboard, which claudex ctl run --dashboard can override; the gateway's
// control status reports the dashboard separately.
func (c Config) Revision() string {
	c.Dashboard = false
	data, _ := json.Marshal(c)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
