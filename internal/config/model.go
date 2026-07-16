package config

import "time"

type Config struct {
	Version        int                 `yaml:"version" json:"version"`
	DefaultProfile string              `yaml:"default_profile" json:"default_profile"`
	Defaults       Defaults            `yaml:"defaults" json:"defaults"`
	Profiles       map[string]Profile  `yaml:"profiles" json:"profiles"`
}

type Defaults struct {
	BindAddress       string        `yaml:"bind_address" json:"bind_address"`
	ProxyPort         int           `yaml:"proxy_port" json:"proxy_port"`
	IdleTunnelTimeout time.Duration `yaml:"idle_tunnel_timeout" json:"idle_tunnel_timeout"`
	LeaseTTL          time.Duration `yaml:"lease_ttl" json:"lease_ttl"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval" json:"heartbeat_interval"`
}

type Profile struct {
	AccountID  string `yaml:"account_id" json:"account_id"`
	ZoneID     string `yaml:"zone_id" json:"zone_id"`
	Domain     string `yaml:"domain" json:"domain"`
	Wildcard   string `yaml:"wildcard" json:"wildcard"`
	TunnelID   string `yaml:"tunnel_id" json:"tunnel_id"`
	TunnelName string `yaml:"tunnel_name" json:"tunnel_name"`
}

func Default() Config {
	return Config{
		Version:        1,
		DefaultProfile: "personal",
		Defaults: Defaults{
			BindAddress:       "127.0.0.1",
			ProxyPort:         4041,
			IdleTunnelTimeout: 60 * time.Second,
			LeaseTTL:          45 * time.Second,
			HeartbeatInterval: 15 * time.Second,
		},
		Profiles: map[string]Profile{},
	}
}
