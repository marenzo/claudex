package handoff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marenzo/claudex/internal/config"
)

//go:embed all:codex-skill
var codexSkill embed.FS

// InstallCodexSkill publishes an immutable namespaced bundle through Codex's
// user skill directory. Switching the symlink atomically avoids partial updates.
func InstallCodexSkill(ctx context.Context, home, binary string) (string, error) {
	if !filepath.IsAbs(binary) {
		return "", errors.New("skill requires an absolute claudex executable path")
	}
	var err error
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ".agents", "skills", "claudex-handoff")
	bundles := filepath.Join(home, ".local", "share", "claudex", "codex-skills")
	files := map[string][]byte{}
	for _, name := range []string{"SKILL.md", ".codex-plugin/plugin.json"} {
		data, err := codexSkill.ReadFile("codex-skill/" + name)
		if err != nil {
			return "", err
		}
		files[name] = bytes.ReplaceAll(data, []byte("{{CLAUDEX_BINARY}}"), []byte(Quote(binary)))
	}
	hash := sha256.New()
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		hash.Write([]byte(name))
		hash.Write(files[name])
	}
	bundle := filepath.Join(bundles, hex.EncodeToString(hash.Sum(nil)))
	err = config.WithLock(ctx, root+"-install", func() error {
		if _, err := os.Lstat(root); err == nil {
			info, err := os.Lstat(root)
			if err != nil {
				return err
			}
			resolved, err := filepath.EvalSymlinks(root)
			if err != nil || info.Mode()&os.ModeSymlink == 0 || !within(bundles, resolved) {
				return fmt.Errorf("refusing to replace a user-owned Codex skill: %s", root)
			}
			data, err := readBounded(filepath.Join(resolved, ".claudex-owned.json"), 64<<10)
			var owned map[string]string
			if err != nil || json.Unmarshal(data, &owned) != nil || len(owned) != len(files) {
				return errors.New("the Codex skill ownership could not be verified")
			}
			for _, name := range names {
				data, err := readBounded(filepath.Join(resolved, name), maxContext)
				if err != nil || digest(data) != owned[name] {
					return fmt.Errorf("the Codex skill was edited; keeping it unchanged: %s", root)
				}
			}
			if err := filepath.WalkDir(resolved, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					return nil
				}
				rel, err := filepath.Rel(resolved, path)
				if err != nil {
					return err
				}
				if rel != ".claudex-owned.json" && owned[filepath.ToSlash(rel)] == "" {
					return fmt.Errorf("the Codex skill contains user files; keeping it unchanged: %s", path)
				}
				return nil
			}); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		owned := map[string]string{}
		for _, name := range names {
			if err := immutableFile(filepath.Join(bundle, name), files[name]); err != nil {
				return err
			}
			owned[name] = digest(files[name])
		}
		data, _ := json.Marshal(owned)
		if err := immutableFile(filepath.Join(bundle, ".claudex-owned.json"), data); err != nil {
			return err
		}
		link, err := os.CreateTemp(filepath.Dir(root), ".claudex-skill-*")
		if err != nil {
			return err
		}
		temporary := link.Name()
		_ = link.Close()
		_ = os.Remove(temporary)
		defer os.Remove(temporary)
		if err := os.Symlink(bundle, temporary); err != nil {
			return err
		}
		return os.Rename(temporary, root)
	})
	return root, err
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// immutableFile never overwrites a changed snapshot or asset, even during races.
func immutableFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Link(file.Name(), path); err == nil {
		return nil
	}
	if !os.IsExist(err) {
		return err
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("snapshot is not a regular file: %s", path)
	}
	existing, readErr := readBounded(path, len(data)+1)
	if readErr != nil || !bytes.Equal(existing, data) {
		return fmt.Errorf("snapshot was modified: %s", path)
	}
	return nil
}

// ClaudeEnvironment removes stale source-session bindings before starting a new
// claudex client; the launcher establishes its own provider routing and hooks.
func ClaudeEnvironment(env []string) []string {
	var out []string
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if name == "CODEX_THREAD_ID" || name == "CODEX_SESSION_ID" || name == "CLAUDEX_TRANSCRIPT_PATH" || name == "CLAUDEX_SESSION_CWD" || name == "CLAUDEX_BINARY" || name == "CLAUDE_CONFIG_DIR" || name == "CLAUDE_CODE_PROJECT_DIR_NAME" || name == "CLAUDEX_CONFIG" {
			continue
		}
		out = append(out, entry)
	}
	return out
}
