package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/nexusriot/ucmon/internal/config"
)

// CheckConfig renders the --check-config report for one config file and says
// whether everything in it was understood. An unrecognised key never stops
// the monitor — a file written for a newer ucmon has to keep working — so
// this flag exists to make the same lines visible on demand, and its exit
// status is what a packaging script can gate on.
func CheckConfig(path string) (string, bool) {
	var b strings.Builder

	if path == "" {
		b.WriteString("ucmon: no config path (and no home directory to derive one)\n")
		b.WriteString("built-in defaults apply\n")
		return b.String(), true
	}

	fmt.Fprintf(&b, "config: %s\n", path)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			b.WriteString("  not present — built-in defaults apply\n")
			return b.String(), true
		}
		fmt.Fprintf(&b, "  unreadable: %v — built-in defaults apply\n", err)
		return b.String(), true
	}

	cfg := config.LoadFrom(path)
	if len(cfg.Unknown) == 0 {
		b.WriteString("  no unrecognised keys\n")
		return b.String(), true
	}

	fmt.Fprintf(&b, "  %s, ignored:\n", pluralKeys(len(cfg.Unknown)))
	for _, u := range cfg.Unknown {
		fmt.Fprintf(&b, "    line %-4d %s\n", u.Line, u.Key)
	}
	b.WriteString("  ucmon runs anyway: unknown keys are never fatal, so a config\n")
	b.WriteString("  written for a newer release keeps working. Check the spelling\n")
	b.WriteString("  against the Configuration section of README.md.\n")
	return b.String(), false
}

// unknownKeysNotice is the one-line startup warning naming the keys the
// config file set and ucmon ignored. Empty when there were none.
func unknownKeysNotice(cfg config.Config) string {
	names := cfg.UnknownKeyNames()
	if len(names) == 0 {
		return ""
	}
	const show = 3
	listed := names
	suffix := ""
	if len(names) > show {
		listed = names[:show]
		suffix = fmt.Sprintf(" +%d more", len(names)-show)
	}
	return fmt.Sprintf("config: %s ignored (%s%s) — run ucmon --check-config",
		pluralKeys(len(names)), strings.Join(listed, ", "), suffix)
}

// pluralKeys counts unrecognised keys without the "(s)" shorthand.
func pluralKeys(n int) string {
	if n == 1 {
		return "1 unrecognised key"
	}
	return fmt.Sprintf("%d unrecognised keys", n)
}
