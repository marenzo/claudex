package proxy

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/marenzo/claudex/internal/metrics"
)

const reviewCacheSessions = 32
const reviewCacheMessages = 256

type cachePrefix struct {
	prefix, context  string
	messages         []string
	sequence         uint64
	endpoint         string
	endpointMessages int
}

// Only keyed fingerprints survive a call. The random key exists for this
// process only, so low-entropy rules and paths cannot be guessed from the log.
// Entries and sampled message boundaries are bounded independently of sessions.
type reviewCacheTracker struct {
	mu       sync.Mutex
	key      []byte
	sequence uint64
	entries  map[string]cachePrefix
}

func (t *reviewCacheTracker) observe(session, model string, input []classifierEvidenceMessage, permissions json.RawMessage) *metrics.CacheDiagnostics {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.key == nil {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil
		}
		t.key = key
		t.entries = make(map[string]cachePrefix)
	}
	digest := func(data []byte) string {
		h := hmac.New(sha256.New, t.key)
		_, _ = h.Write(data)
		return hex.EncodeToString(h.Sum(nil)[:16])
	}
	identity := digest([]byte(model + "\x00" + session))
	messageBytes := func(message classifierEvidenceMessage) []byte {
		raw, _ := json.Marshal(message)
		return raw
	}
	first := messageBytes(input[0])
	base := model + "\x00" + classifierReasoningEffort + "\x00" + classifierPolicy + "\x00"
	view := &metrics.CacheDiagnostics{Affinity: identity, Prefix: digest(append([]byte(base), first...)), Context: digest(permissions), InputMessages: len(input), Truncated: len(input) > reviewCacheMessages}
	current := cachePrefix{prefix: view.Prefix, context: view.Context}
	previous, exists := t.entries[identity]
	view.Previous = exists
	view.PrefixChanged = exists && previous.prefix != view.Prefix
	view.ContextChanged = exists && previous.context != view.Context
	shared := exists && !view.PrefixChanged
	chain := view.Prefix
	for i, message := range input {
		raw := messageBytes(message)
		chain = digest(append([]byte(chain), raw...))
		if i < reviewCacheMessages {
			current.messages = append(current.messages, chain)
		}
		if i < reviewCacheMessages && shared && i < len(previous.messages) && previous.messages[i] == chain {
			view.SharedMessages++
			view.SharedBytes += len(raw)
		} else {
			shared = false
		}
		if exists && !view.PrefixChanged && previous.endpointMessages == i+1 && previous.endpoint == chain {
			view.PreviousEndpointMatched = true
		}
		if message.Role == "user" {
			current.endpoint, current.endpointMessages = chain, i+1
		}
	}
	view.ComparedMessages = min(len(current.messages), len(previous.messages))
	t.sequence++
	current.sequence = t.sequence
	if !exists && len(t.entries) >= reviewCacheSessions {
		var oldest string
		var sequence uint64 = ^uint64(0)
		for key, entry := range t.entries {
			if entry.sequence < sequence {
				oldest, sequence = key, entry.sequence
			}
		}
		delete(t.entries, oldest)
	}
	t.entries[identity] = current
	return view
}
