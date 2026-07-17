package leases

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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
	mu         sync.Mutex
	leases     map[string]*Lease
	byKey      map[string]string // host+path -> lease id
	owners     map[string]map[string]struct{}
	registry   *router.Registry
	storeDir   string
	defaultTTL time.Duration
	initErr    error
	lastErr    error
}

func NewManager(reg *router.Registry, storeDir string, ttl time.Duration) *Manager {
	if ttl == 0 {
		ttl = 45 * time.Second
	}
	m := &Manager{
		leases:     make(map[string]*Lease),
		byKey:      make(map[string]string),
		owners:     make(map[string]map[string]struct{}),
		registry:   reg,
		storeDir:   storeDir,
		defaultTTL: ttl,
	}
	m.initErr = m.purgeDiskLeases()
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
		if err := os.Remove(filepath.Join(m.storeDir, e.Name())); err != nil {
			return err
		}
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
	if err := m.ready(); err != nil {
		return Lease{}, err
	}

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
			if err := m.releaseLocked(existing.ID, existing.OwnerToken, true); err != nil {
				return Lease{}, err
			}
		} else if req.Reuse {
			sameTarget := existing.Target == req.Target &&
				existing.HostHeader == req.HostHeader &&
				existing.Insecure == req.Insecure
			if !sameTarget {
				return Lease{}, apperr.New(apperr.ExitConflict,
					fmt.Sprintf("%q is already active with a different target", req.Hostname))
			}
			token, err := randomToken()
			if err != nil {
				return Lease{}, err
			}
			previousRenewedAt := existing.RenewedAt
			previousExpiresAt := existing.ExpiresAt
			now := time.Now()
			existing.RenewedAt = now
			existing.ExpiresAt = now.Add(ttl)
			if err := m.persist(existing); err != nil {
				existing.RenewedAt = previousRenewedAt
				existing.ExpiresAt = previousExpiresAt
				return Lease{}, fmt.Errorf("persist reused lease: %w", err)
			}
			if m.owners[existing.ID] == nil {
				m.owners[existing.ID] = map[string]struct{}{existing.OwnerToken: {}}
			}
			m.owners[existing.ID][token] = struct{}{}
			shared := *existing
			shared.OwnerToken = token
			return shared, nil
		} else if !req.Replace {
			return Lease{}, apperr.New(apperr.ExitConflict, fmt.Sprintf(
				"%q is already active.\n\nTarget    %s\nProcess   %d\nStarted   %s\n\nUse --replace to take over the hostname.",
				req.Hostname, existing.Target, existing.OwnerPID, existing.CreatedAt.Format(time.RFC3339)))
		} else {
			if err := m.releaseLocked(existing.ID, existing.OwnerToken, true); err != nil {
				return Lease{}, err
			}
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
		ID:             uuid.NewString(),
		RouteID:        uuid.NewString(),
		Hostname:       req.Hostname,
		PathPrefix:     req.PathPrefix,
		Target:         req.Target,
		HostHeader:     req.HostHeader,
		OwnerPID:       req.OwnerPID,
		OwnerStartTime: req.OwnerStartTime,
		OwnerToken:     token,
		CreatedAt:      now,
		RenewedAt:      now,
		ExpiresAt:      now.Add(ttl),
		Ephemeral:      req.Ephemeral,
		Insecure:       req.Insecure,
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
	if err := m.persist(lease); err != nil {
		cleanupErr := m.registry.Remove(lease.RouteID)
		return Lease{}, errors.Join(
			fmt.Errorf("persist lease: %w", err),
			cleanupErr,
		)
	}
	m.leases[lease.ID] = lease
	m.byKey[k] = lease.ID
	m.owners[lease.ID] = map[string]struct{}{lease.OwnerToken: {}}
	return *lease, nil
}

func (m *Manager) Renew(id, token string, ttl time.Duration) (Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ready(); err != nil {
		return Lease{}, err
	}
	l, ok := m.leases[id]
	if !ok {
		return Lease{}, apperr.New(apperr.ExitDaemon, "lease not found")
	}
	if _, ok := m.owners[id][token]; !ok {
		return Lease{}, apperr.New(apperr.ExitAuth, "invalid owner token")
	}
	if ttl == 0 {
		ttl = m.defaultTTL
	}
	previousRenewedAt := l.RenewedAt
	previousExpiresAt := l.ExpiresAt
	now := time.Now()
	l.RenewedAt = now
	l.ExpiresAt = now.Add(ttl)
	if err := m.persist(l); err != nil {
		l.RenewedAt = previousRenewedAt
		l.ExpiresAt = previousExpiresAt
		return Lease{}, fmt.Errorf("persist renewed lease: %w", err)
	}
	renewed := *l
	renewed.OwnerToken = token
	return renewed, nil
}

func (m *Manager) Release(id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ready(); err != nil {
		return err
	}
	l, ok := m.leases[id]
	if !ok {
		return nil
	}
	if _, ok := m.owners[id][token]; !ok {
		return apperr.New(apperr.ExitAuth, "invalid owner token")
	}
	delete(m.owners[id], token)
	if len(m.owners[id]) > 0 {
		if err := m.persist(l); err != nil {
			m.owners[id][token] = struct{}{}
			return fmt.Errorf("persist released lease owner: %w", err)
		}
		return nil
	}
	err := m.releaseLocked(id, "", true)
	if err != nil {
		m.owners[id][token] = struct{}{}
	}
	return err
}

// ForceRelease revokes a lease without the owner token (local daemon admin).
func (m *Manager) ForceRelease(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.releaseLocked(id, "", true)
}

func (m *Manager) ready() error {
	if m.initErr != nil {
		return fmt.Errorf("initialize lease store: %w", m.initErr)
	}
	return nil
}

// LastError returns the most recent reconciliation or expiry cleanup error.
func (m *Manager) LastError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

func (m *Manager) releaseLocked(id, token string, force bool) error {
	l, ok := m.leases[id]
	if !ok {
		return nil
	}
	if !force && l.OwnerToken != token {
		return apperr.New(apperr.ExitAuth, "invalid owner token")
	}
	var cleanupErr error
	if err := m.registry.Remove(l.RouteID); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove route: %w", err))
	}
	if m.storeDir != "" {
		err := os.Remove(filepath.Join(m.storeDir, id+".json"))
		if err != nil && !os.IsNotExist(err) {
			cleanupErr = errors.Join(
				cleanupErr,
				fmt.Errorf("remove persisted lease: %w", err),
			)
		}
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	delete(m.leases, id)
	delete(m.byKey, key(l.Hostname, l.PathPrefix))
	delete(m.owners, id)
	return cleanupErr
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

func (m *Manager) Reconcile() bool {
	changed, _ := m.ReconcileWithError()
	return changed
}

// ReconcileWithError removes expired or dead-owner leases and reports cleanup
// failures while preserving the existing Reconcile behavior for callers that
// only need the changed flag.
func (m *Manager) ReconcileWithError() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed, err := m.reconcileLocked()
	m.lastErr = err
	return changed, err
}

func (m *Manager) reconcileLocked() (bool, error) {
	now := time.Now()
	changed := false
	var reconcileErr error
	for id, l := range m.leases {
		stale := !l.ExpiresAt.After(now)
		if !stale && l.OwnerPID > 0 {
			stale = !procutil.Alive(l.OwnerPID)
			if !stale && l.OwnerStartTime > 0 {
				startTime, err := procutil.StartTime(l.OwnerPID)
				stale = err == nil && startTime != l.OwnerStartTime
			}
		}
		if stale {
			if err := m.releaseLocked(id, l.OwnerToken, true); err != nil {
				reconcileErr = errors.Join(
					reconcileErr,
					fmt.Errorf("reconcile lease %q: %w", id, err),
				)
			}
			changed = true
		}
	}
	return changed, reconcileErr
}

func (m *Manager) ExpireLoop(stop <-chan struct{}, interval time.Duration, callbacks ...func(bool)) {
	m.ExpireLoopWithError(stop, interval, func(changed bool, _ error) {
		for _, callback := range callbacks {
			if callback != nil {
				callback(changed)
			}
		}
	})
}

// ExpireLoopWithError is the error-reporting variant of ExpireLoop.
func (m *Manager) ExpireLoopWithError(
	stop <-chan struct{},
	interval time.Duration,
	callback func(bool, error),
) {
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
			changed, err := m.ReconcileWithError()
			if callback != nil {
				callback(changed, err)
			}
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
	info, err := os.Lstat(m.storeDir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("lease store is not a directory: %q", m.storeDir)
	}
	if err := os.Chmod(m.storeDir, 0o700); err != nil {
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
