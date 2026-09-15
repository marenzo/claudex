package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k"}`, true},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k","reasoning_effort":"ultra"}`, true},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k","reasoning_effort":"max"}`, true},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k","listen":"0.0.0.0:8317"}`, false},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k","listen":"127.0.0.1:1"}`, true},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k","listen":"[::1]:65535"}`, true},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k","listen":"127.0.0.1:"}`, false},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k","listen":"127.0.0.1:http"}`, false},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k","listen":"127.0.0.1:0"}`, false},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k","listen":"127.0.0.1:-1"}`, false},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k","listen":"127.0.0.1:65536"}`, false},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k","listen":"[::1%lo0]:8317"}`, false},
		{`{"auth_file":"relative","client_key_file":"/tmp/k"}`, false},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k","compact_window":1000000}`, false},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k","dashboard":true}`, false},
		{`{"auth_file":"/tmp/a","client_key_file":"/tmp/k"} {}`, false},
	} {
		p := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(p, []byte(tc.body), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		if (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.body, err)
		}
	}
}
