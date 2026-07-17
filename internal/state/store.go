package state

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"portx/internal/config"
)

type Store struct {
	mu   sync.Mutex
	path string
	data Data
}

type Data struct {
	Version  int                     `json:"version"`
	Phase    string                  `json:"phase,omitempty"`
	Profiles map[string]ProfileState `json:"profiles"`
}

type ProfileState struct {
	TunnelID    string     `json:"tunnel_id,omitempty"`
	WildcardDNS *DNSRecord `json:"wildcard_dns,omitempty"`
}

type DNSRecord struct {
	RecordID     string `json:"record_id"`
	Hostname     string `json:"hostname"`
	OwnedByPortx bool   `json:"owned_by_portx"`
}

const (
	PhaseNone          = "none"
	PhaseAuthenticated = "authenticated"
	PhaseSelected      = "resources_selected"
	PhaseTunnelEnsured = "tunnel_ensured"
	PhaseTokenStored   = "token_stored"
	PhaseConfigApplied = "config_applied"
	PhaseDNSEnsured    = "dns_ensured"
	PhaseVerified      = "verified"
	PhaseReady         = "ready"
)

func Open() (*Store, error) {
	path, err := config.StateFile()
	if err != nil {
		return nil, err
	}
	s := &Store{path: path, data: Data{Version: 1, Phase: PhaseNone, Profiles: map[string]ProfileState{}}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, err
	}
	if s.data.Profiles == nil {
		s.data.Profiles = map[string]ProfileState{}
	}
	return s, nil
}

func (s *Store) Data() Data {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.data
	out.Profiles = maps.Clone(s.data.Profiles)
	if out.Profiles == nil {
		out.Profiles = map[string]ProfileState{}
	}
	return out
}

func (s *Store) SetPhase(phase string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Phase = phase
	return s.persist()
}

func (s *Store) PutProfile(name string, ps ProfileState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Profiles[name] = ps
	return s.persist()
}

func (s *Store) Replace(data Data) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if data.Profiles == nil {
		data.Profiles = map[string]ProfileState{}
	}
	if data.Version == 0 {
		data.Version = 1
	}
	s.data = data
	return s.persist()
}

func (s *Store) persist() error {
	if err := config.EnsureDir(filepath.Dir(s.path)); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, b)
}

func writeFileAtomic(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to replace symlink: %q", path)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}
