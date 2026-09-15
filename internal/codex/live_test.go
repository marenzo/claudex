//go:build live

package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/marenzo/claudex/internal/auth"
)

// Opt-in read-only integration check; use a temporary credential without a
// refresh_token so testing cannot rotate a running service's sign-in.
func TestLiveQuotaEndpoint(t *testing.T) {
	path := os.Getenv("CLAUDEX_TEST_AUTH_FILE")
	if path == "" {
		t.Skip("live subscription check not requested")
	}
	credential, err := readOnlyCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	// Isolate the validated snapshot from a caller changing the source file while
	// the check runs. No network path can obtain a refresh token from this store.
	fixture := filepath.Join(t.TempDir(), "auth.json")
	data, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.WritePrivate(fixture, data); err != nil {
		t.Fatal(err)
	}
	store := auth.NewStore(fixture)
	allowed, err := NewClient(store).QuotaAvailable(context.Background(), credential.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("quota allowed: %t", allowed)
}

func readOnlyCredential(path string) (auth.Credential, error) {
	var credential auth.Credential
	data, err := os.ReadFile(path)
	if err != nil {
		return credential, err
	}
	if err := json.Unmarshal(data, &credential); err != nil {
		return credential, err
	}
	if credential.RefreshToken != "" {
		return auth.Credential{}, fmt.Errorf("live fixture must omit refresh_token")
	}
	return credential, nil
}

func TestReadOnlyFixtureRejectsRefreshBeforeAcquisition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	data := []byte(`{"type":"codex","account_id":"a","access_token":"old","refresh_token":"must-not-use","expired":"2000-01-01T00:00:00Z"}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	credential, err := readOnlyCredential(path)
	if err == nil || credential.RefreshToken != "" {
		t.Fatal("accepted a fixture that could rotate a real credential")
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != string(data) {
		t.Fatal("changed the original credential")
	}
}
