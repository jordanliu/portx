package config

// ResolveProfile picks the active profile name.
// flag wins when non-empty; otherwise project, then default_profile, then "personal".
func ResolveProfile(flag, projectProfile, defaultProfile string) string {
	if flag != "" {
		return flag
	}
	if projectProfile != "" {
		return projectProfile
	}
	if defaultProfile != "" {
		return defaultProfile
	}
	return "personal"
}
