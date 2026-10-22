package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Revision fingerprints all persisted gateway settings.
func (c Config) Revision() string {
	data, _ := json.Marshal(c)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
