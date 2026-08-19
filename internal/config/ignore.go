package config

import "strings"

// HideIface reports whether a network interface is filtered out by net_ignore.
func (c Config) HideIface(name string) bool { return anyGlob(c.NetIgnore, name) }

// HideMount reports whether a filesystem is filtered out by disk_ignore. It
// matches on the mount point, which is stable across reboots in a way device
// names are not.
func (c Config) HideMount(mount string) bool { return anyGlob(c.DiskIgnore, mount) }

// HideSensor reports whether a temperature sensor is filtered out by
// temp_ignore, matching the label ucmon displays.
func (c Config) HideSensor(label string) bool { return anyGlob(c.TempIgnore, label) }

func anyGlob(patterns []string, s string) bool {
	for _, p := range patterns {
		if globMatch(p, s) {
			return true
		}
	}
	return false
}

// globMatch reports whether s matches pattern, where "*" stands for any run of
// characters. Unlike path.Match, "*" spans "/" as well, so "/snap/*" hides a
// whole mount subtree rather than only its immediate children — which is what
// someone writing an ignore list expects.
func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}

	first, last := parts[0], parts[len(parts)-1]
	if !strings.HasPrefix(s, first) {
		return false
	}
	rest := s[len(first):]

	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(rest, mid)
		if i < 0 {
			return false
		}
		rest = rest[i+len(mid):]
	}
	return strings.HasSuffix(rest, last)
}

// splitList parses a comma-separated pattern list, dropping empty entries so
// a trailing comma or an all-blank value disables the filter.
func splitList(val string) []string {
	var out []string
	for _, p := range strings.Split(val, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
