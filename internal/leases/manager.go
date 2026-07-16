package leases

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"portx/internal/apperr"
	"portx/internal/procutil"
	"portx/internal/router"
)

type Manager struct {
	mu       sync.Mutex
	leases   map[string]*Lease
	byKey    map[string]string // host+path -> lease id
	registry *router.Registry
	storeDir string
	defaultTTL time.Duration
}

func NewManager(reg *router.Registry, storeDir string, ttl time.Duration) *Manager {
	if ttl == 0 {
		ttl = 45 * time.Second
	}
	m := &Manager{
		leases:     make(map[string]*Lease),
		byKey:      make(map[string]string),
		registry:   reg,
		storeDir:   storeDir,
		defaultTTL: ttl,
	}
	_ = m.purgeDiskLeases()
	return m
}

// purgeDiskLeases removes stale lease files after daemon restart.
// Owner tokens are memory-only, so disk leases cannot be safely restored.
func (m *Manager) purgeDiskLeases() error {
	if m.storeDir == "" {
		return nil
	}
	entries, err := os.ReadDir(m.storeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		_ = os.Remove(filepath.Join(m.storeDir, e.Name()))
	}
	return nil
}

func key(host, path string) string {
	if path == "" {
		path = "/"
	}
	return host + "\x00" + path
}

func (m *Manager) Acquire(req AcquireRequest) (Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ttl := req.TTL
	if ttl == 0 {
		ttl = m.defaultTTL
	}
	if req.PathPrefix == "" {
		req.PathPrefix = "/"
	}
	k := key(req.Hostname, req.PathPrefix)
	if existingID, ok := m.byKey[k]; ok {
		existing := m.leases[existingID]
		if existing == nil {
			delete(m.byKey, k)
		} else if !existing.ExpiresAt.After(time.Now()) {
			_ = m.releaseLocked(existing.ID, existing.OwnerToken, true)
		} else if req.Reuse {
			sameTarget := existing.Target == req.Target && existing.HostHeader == req.HostHeader
			if !sameTarget {
				return Lease{}, apperr.New(apperr.ExitConflict,
					fmt.Sprintf("%q is already active with a different target", req.Hostname))
			}
			existing.RenewedAt = time.Now()
			existing.ExpiresAt = time.Now().Add(ttl)
			_ = m.persist(existing)
			return *existing, nil
		} else if !req.Replace {
			return Lease{}, apperr.New(apperr.ExitConflict, fmt.Sprintf(
				"%q is already active.\n\nTarget    %s\nProcess   %d\nStarted   %s\n\nUse --replace to take over the hostname.",
				req.Hostname, existing.Target, existing.OwnerPID, existing.CreatedAt.Format(time.RFC3339)))
		} else {
			_ = m.releaseLocked(existing.ID, existing.OwnerToken, true)
		}
	}

	target, err := url.Parse(req.Target)
	if err != nil {
		return Lease{}, apperr.Wrap(apperr.ExitInvalidArgs, "parse target", err)
	}
	token, err := randomToken()
	if err != nil {
		return Lease{}, err
	}
	now := time.Now()
	lease := &Lease{
		ID:         uuid.NewString(),
		RouteID:    uuid.NewString(),
		Hostname:   req.Hostname,
		PathPrefix: req.PathPrefix,
		Target:     req.Target,
		HostHeader: req.HostHeader,
		OwnerPID:   req.OwnerPID,
		OwnerToken: token,
		CreatedAt:  now,
		RenewedAt:  now,
		ExpiresAt:  now.Add(ttl),
		Ephemeral:  req.Ephemeral,
		Insecure:   req.Insecure,
	}
	if err := m.registry.Add(router.Route{
		ID:         lease.RouteID,
		Hostname:   lease.Hostname,
		PathPrefix: lease.PathPrefix,
		Target:     target,
		HostHeader: lease.HostHeader,
		TLS:        router.OriginTLS{InsecureSkipVerify: lease.Insecure},
		CreatedAt:  now,
	}); err != nil {
		return Lease{}, err
	}
	m.leases[lease.ID] = lease
	m.byKey[k] = lease.ID
	_ = m.persist(lease)
	return *lease, nil
}

func (m *Manager) Renew(id, token string, ttl time.Duration) (Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[id]
	if !ok {
		return Lease{}, apperr.New(apperr.ExitDaemon, "lease not found")
	}
	if l.OwnerToken != token {
		return Lease{}, apperr.New(apperr.ExitAuth, "invalid owner token")
	}
	if ttl == 0 {
		ttl = m.defaultTTL
	}
	l.RenewedAt = time.Now()
	l.ExpiresAt = time.Now().Add(ttl)
	_ = m.persist(l)
	return *l, nil
}

func (m *Manager) Release(id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.releaseLocked(id, token, false)
}

// ForceRelease revokes a lease without the owner token (local daemon admin).
func (m *Manager) ForceRelease(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.releaseLocked(id, "", true)
}

func (m *Manager) releaseLocked(id, token string, force bool) error {
	l, ok := m.leases[id]
	if !ok {
		return nil
	}
	if !force && l.OwnerToken != token {
		return apperr.New(apperr.ExitAuth, "invalid owner token")
	}
	_ = m.registry.Remove(l.RouteID)
	delete(m.leases, id)
	delete(m.byKey, key(l.Hostname, l.PathPrefix))
	if m.storeDir != "" {
		_ = os.Remove(filepath.Join(m.storeDir, id+".json"))
	}
	return nil
}

func (m *Manager) List() []Lease {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Lease, 0, len(m.leases))
	for _, l := range m.leases {
		out = append(out, *l)
	}
	return out
}

func (m *Manager) Reconcile() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, l := range m.leases {
		stale := !l.ExpiresAt.After(now)
		if !stale && l.OwnerPID > 0 && !procutil.Alive(l.OwnerPID) {
			stale = true
		}
		if stale {
			_ = m.releaseLocked(id, l.OwnerToken, true)
		}
	}
}

func (m *Manager) ExpireLoop(stop <-chan struct{}, interval time.Duration) {
	if interval == 0 {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			m.Reconcile()
		}
	}
}

func (m *Manager) persist(l *Lease) error {
	if m.storeDir == "" {
		return nil
	}
	if err := os.MkdirAll(m.storeDir, 0o700); err != nil {
		return err
	}
	// Never persist owner_token; memory-only after acquire.
	disk := *l
	disk.OwnerToken = ""
	path := filepath.Join(m.storeDir, l.ID+".json")
	return writeJSON(path, &disk)
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
