package feedback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/marenzo/claudex/internal/config"
)

const MaxRecords = 1000
const Retention = 30 * 24 * time.Hour

var ErrFull = errors.New("feedback store is full; export or remove old records before retrying")
var validID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var validLaunchID = regexp.MustCompile(`^[a-f0-9]{32}$`)

// Source is attached by the launcher/MCP bridge, not supplied in the tool schema.
// LaunchID identifies a CLI launch, not a Claude conversation or upstream request.
type Source struct {
	LaunchID      string `json:"launch_id"`
	ClientVersion string `json:"client_version,omitempty"`
}
type Submission struct {
	Source Source `json:"source"`
	Report Report `json:"report"`
}

func (s Source) Validate() error {
	if !validLaunchID.MatchString(s.LaunchID) {
		return errors.New("invalid launch_id")
	}
	if s.ClientVersion != "" {
		if err := textField(s.ClientVersion, 100); err != nil {
			return errors.New("invalid client_version")
		}
	}
	return nil
}
func (s Submission) Validate() error {
	if err := s.Source.Validate(); err != nil {
		return err
	}
	return s.Report.Validate()
}

type Record struct {
	ID              string    `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	State           string    `json:"state"`
	Source          Source    `json:"source"`
	GatewayVersion  string    `json:"gateway_version"`
	ConfiguredModel string    `json:"configured_model"` // explicitly NOT the actual generating model
	Report          Report    `json:"report"`
}
type Receipt struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	Duplicate bool   `json:"duplicate"`
}
type Store struct{ Dir string }

func recordID(s Submission) string {
	// Client version can change on MCP reconnect without creating a new incident.
	raw, _ := json.Marshal(struct {
		Launch string
		Report Report
	}{s.Source.LaunchID, s.Report})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
func (s Store) locked(ctx context.Context, fn func() error) error {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(s.Dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("feedback directory must be a real directory")
	}
	if err := os.Chmod(s.Dir, 0700); err != nil {
		return err
	}
	return config.WithLock(ctx, filepath.Join(s.Dir, "store"), fn)
}
func (s Store) records(now time.Time) ([]Record, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0)
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".json" || !validID.MatchString(name[:len(name)-5]) {
			continue
		}
		if !e.Type().IsRegular() {
			return nil, errors.New("unexpected feedback record type")
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		if info.Size() > 32<<10 {
			return nil, errors.New("oversized feedback record")
		}
		raw, err := os.ReadFile(filepath.Join(s.Dir, name))
		if err != nil {
			return nil, err
		}
		var r Record
		if json.Unmarshal(raw, &r) != nil || r.ID+".json" != name || r.CreatedAt.IsZero() {
			return nil, errors.New("invalid feedback record")
		}
		if now.Sub(r.CreatedAt) > Retention {
			if err := os.Remove(filepath.Join(s.Dir, name)); err != nil {
				return nil, err
			}
			continue
		}
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	return records, nil
}
func (s Store) List(ctx context.Context) (out []Record, err error) {
	err = s.locked(ctx, func() error { var e error; out, e = s.records(time.Now().UTC()); return e })
	return
}
func (s Store) Put(ctx context.Context, sub Submission, version, model string) (receipt Receipt, err error) {
	if err = sub.Validate(); err != nil {
		return
	}
	err = s.locked(ctx, func() error {
		now := time.Now().UTC()
		records, e := s.records(now)
		if e != nil {
			return e
		}
		id := recordID(sub)
		for _, r := range records {
			if r.ID == id {
				receipt = Receipt{id, r.State, true}
				return nil
			}
		}
		if len(records) >= MaxRecords {
			return ErrFull
		}
		r := Record{id, now, "untriaged", sub.Source, version, model, sub.Report}
		raw, e := json.MarshalIndent(r, "", "  ")
		if e != nil {
			return e
		}
		if e = durableWrite(filepath.Join(s.Dir, id+".json"), append(raw, '\n')); e != nil {
			return e
		}
		receipt = Receipt{id, r.State, false}
		return nil
	})
	return
}
func durableWrite(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".feedback-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
