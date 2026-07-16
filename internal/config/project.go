package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"portx/internal/apperr"
)

const ProjectFileName = "portx.yaml"

// legacyProjectFileName is still discovered for older checkouts.
const legacyProjectFileName = ".portx.yaml"

type ProjectConfig struct {
	Version int                     `yaml:"version" json:"version"`
	Project string                  `yaml:"project" json:"project"`
	Profile string                  `yaml:"profile,omitempty" json:"profile,omitempty"`
	Routes  map[string]ProjectRoute `yaml:"routes" json:"routes"`
}

type ProjectRoute struct {
	Target     string `yaml:"target" json:"target"`
	Hostname   string `yaml:"hostname" json:"hostname"`
	Path       string `yaml:"path,omitempty" json:"path,omitempty"`
	HostHeader string `yaml:"host_header,omitempty" json:"host_header,omitempty"`
	Insecure   bool   `yaml:"insecure_skip_verify,omitempty" json:"insecure_skip_verify,omitempty"`
}

func FindProjectFile(startDir string) (string, error) {
	dir := startDir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	for {
		for _, name := range []string{ProjectFileName, legacyProjectFileName} {
			candidate := filepath.Join(dir, name)
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				return candidate, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func LoadProject(path string) (ProjectConfig, error) {
	var pc ProjectConfig
	if path == "" {
		found, err := FindProjectFile("")
		if err != nil {
			return pc, err
		}
		path = found
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return pc, err
	}
	if err := yaml.Unmarshal(data, &pc); err != nil {
		return pc, apperr.Wrap(apperr.ExitInvalidArgs, "parse project config", err)
	}
	if pc.Version == 0 {
		pc.Version = 1
	}
	if pc.Routes == nil {
		pc.Routes = map[string]ProjectRoute{}
	}
	return pc, nil
}

func SaveProject(path string, pc ProjectConfig) error {
	if path == "" {
		if found, err := FindProjectFile(""); err == nil {
			path = found
		} else {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			path = filepath.Join(wd, ProjectFileName)
		}
	}
	if pc.Version == 0 {
		pc.Version = 1
	}
	if pc.Routes == nil {
		pc.Routes = map[string]ProjectRoute{}
	}
	data, err := yaml.Marshal(pc)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (p ProjectConfig) Validate() error {
	if p.Version != 1 {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("unsupported project config version %d", p.Version))
	}
	if len(p.Routes) == 0 {
		return apperr.New(apperr.ExitInvalidArgs, "project config has no routes")
	}
	for name, r := range p.Routes {
		if strings.TrimSpace(name) == "" {
			return apperr.New(apperr.ExitInvalidArgs, "route name is required")
		}
		if strings.TrimSpace(r.Target) == "" {
			return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("route %q: target is required", name))
		}
		if strings.TrimSpace(r.Hostname) == "" {
			return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("route %q: hostname is required", name))
		}
	}
	return nil
}

func (p *ProjectConfig) UpsertRoute(name string, route ProjectRoute) {
	if p.Routes == nil {
		p.Routes = map[string]ProjectRoute{}
	}
	p.Routes[name] = route
}

type MergedView struct {
	Global  Config        `json:"global" yaml:"global"`
	Project *ProjectConfig `json:"project,omitempty" yaml:"project,omitempty"`
	Profile string        `json:"active_profile" yaml:"active_profile"`
}

func MergeView(global Config, project *ProjectConfig, profile string) MergedView {
	if profile == "" {
		profile = global.DefaultProfile
		if project != nil && project.Profile != "" {
			profile = project.Profile
		}
	}
	return MergedView{Global: global, Project: project, Profile: profile}
}
