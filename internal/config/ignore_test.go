package config

import (
	"reflect"
	"testing"
)

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"veth0", "veth0", true},
		{"veth0", "veth1", false},
		{"veth*", "veth785acc5", true},
		{"veth*", "eth0", false},
		{"br-*", "br-14302e83458c", true},
		{"*", "anything", true},
		{"*", "", true},
		{"docker*", "docker0", true},
		{"*0", "docker0", true},
		{"*0", "docker1", false},
		{"coretemp_core_*", "coretemp_core_12", true},
		{"coretemp_core_*", "coretemp_package_id_0", false},

		// "*" spans "/" so a mount pattern covers the whole subtree — the
		// behaviour path.Match would not give.
		{"/snap/*", "/snap/core22/1621", true},
		{"/snap/*", "/snap", false},
		{"/var/lib/docker/*", "/var/lib/docker/overlay2/abc/merged", true},
		{"/boot*", "/boot/firmware", true},

		// Multiple wildcards.
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "axxc", false},
		{"a*a", "a", false},
		{"", "", true},
		{"", "x", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.s); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

func TestHidePredicates(t *testing.T) {
	cfg := Config{
		NetIgnore:  []string{"veth*", "br-*", "docker0"},
		DiskIgnore: []string{"/snap/*", "/boot/efi"},
		TempIgnore: []string{"coretemp_core_*"},
	}

	for _, c := range []struct {
		name string
		got  bool
		want bool
	}{
		{"veth785acc5", cfg.HideIface("veth785acc5"), true},
		{"br-1430", cfg.HideIface("br-1430"), true},
		{"docker0", cfg.HideIface("docker0"), true},
		{"wlan0", cfg.HideIface("wlan0"), false},
		{"eth0", cfg.HideIface("eth0"), false},
		{"/snap/core22/1", cfg.HideMount("/snap/core22/1"), true},
		{"/boot/efi", cfg.HideMount("/boot/efi"), true},
		{"/", cfg.HideMount("/"), false},
		{"coretemp_core_8", cfg.HideSensor("coretemp_core_8"), true},
		{"cpu_thermal", cfg.HideSensor("cpu_thermal"), false},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}

	// An empty config hides nothing.
	var none Config
	if none.HideIface("veth0") || none.HideMount("/snap/x") || none.HideSensor("anything") {
		t.Error("an unset ignore list hid something")
	}
}

func TestIgnoreListParsing(t *testing.T) {
	cfg := LoadFrom(writeConfig(t, `
net_ignore  = "veth*, br-*, docker0"    # container noise
disk_ignore = /snap/*, /var/lib/docker/*
temp_ignore = "coretemp_core_*"
`))
	if want := []string{"veth*", "br-*", "docker0"}; !reflect.DeepEqual(cfg.NetIgnore, want) {
		t.Errorf("NetIgnore = %q, want %q", cfg.NetIgnore, want)
	}
	if want := []string{"/snap/*", "/var/lib/docker/*"}; !reflect.DeepEqual(cfg.DiskIgnore, want) {
		t.Errorf("DiskIgnore = %q, want %q", cfg.DiskIgnore, want)
	}
	if want := []string{"coretemp_core_*"}; !reflect.DeepEqual(cfg.TempIgnore, want) {
		t.Errorf("TempIgnore = %q, want %q", cfg.TempIgnore, want)
	}
}

// Blank and trailing-comma entries are dropped so they cannot match everything.
func TestIgnoreListDropsBlanks(t *testing.T) {
	cfg := LoadFrom(writeConfig(t, "net_ignore = \"veth*, , docker0,\"\ndisk_ignore = \"  \"\n"))
	if want := []string{"veth*", "docker0"}; !reflect.DeepEqual(cfg.NetIgnore, want) {
		t.Errorf("NetIgnore = %q, want %q", cfg.NetIgnore, want)
	}
	if len(cfg.DiskIgnore) != 0 {
		t.Errorf("an all-blank list became %q", cfg.DiskIgnore)
	}
	if cfg.HideMount("/") {
		t.Error("an all-blank list hid a mount")
	}
}

// The defaults hide nothing: ucmon should show the whole machine until asked
// not to.
func TestDefaultHidesNothing(t *testing.T) {
	cfg := Default()
	if len(cfg.NetIgnore)+len(cfg.DiskIgnore)+len(cfg.TempIgnore) != 0 {
		t.Errorf("default config ships ignore patterns: %q %q %q",
			cfg.NetIgnore, cfg.DiskIgnore, cfg.TempIgnore)
	}
}
