package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"portx/internal/apperr"
)

func Load() (Config, error) {
	cfg := Default()
	path, err := ConfigFile()
	if err != nil {
		return cfg, apperr.Wrap(apperr.ExitInvalidArgs, "resolve config path", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, apperr.Wrap(apperr.ExitInvalidArgs, "read config", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, apperr.Wrap(apperr.ExitInvalidArgs, "parse config", err)
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	def := Default().Defaults
	if cfg.Defaults.ProxyPort == 0 {
		cfg.Defaults.ProxyPort = def.ProxyPort
	}
	if cfg.Defaults.BindAddress == "" {
		cfg.Defaults.BindAddress = def.BindAddress
	}
	if cfg.Defaults.IdleTunnelTimeout <= 0 {
		cfg.Defaults.IdleTunnelTimeout = def.IdleTunnelTimeout
	}
	if cfg.Defaults.LeaseTTL <= 0 {
		cfg.Defaults.LeaseTTL = def.LeaseTTL
	}
	if cfg.Defaults.HeartbeatInterval <= 0 {
		cfg.Defaults.HeartbeatInterval = def.HeartbeatInterval
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	if cfg.DefaultProfile == "" {
		cfg.DefaultProfile = "personal"
	}
	return cfg, nil
}

func Save(cfg Config) error {
	path, err := ConfigFile()
	if err != nil {
		return err
	}
	if err := EnsureDir(filepath.Dir(path)); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o600)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
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

	if err := tmp.Chmod(mode.Perm()); err != nil {
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

func (c Config) Profile(name string) (Profile, error) {
	if name == "" {
		name = c.DefaultProfile
	}
	p, ok := c.Profiles[name]
	if !ok {
		return Profile{}, apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("profile %q not found; run portx setup", name))
	}
	return p, nil
}
