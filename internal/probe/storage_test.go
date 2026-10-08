package probe

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTree creates files under root from a path -> contents map.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadStorageHealthEMMC(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"mmcblk0/size":                "62333952\n",
		"mmcblk0/ro":                  "0\n",
		"mmcblk0/device/type":         "MMC\n",
		"mmcblk0/device/name":         "BJTD4R\n",
		"mmcblk0/device/serial":       "0xdeadbeef\n",
		"mmcblk0/device/life_time":    "0x03 0x02\n",
		"mmcblk0/device/pre_eol_info": "0x02\n",
		"loop0/size":                  "1234\n",
		"zram0/size":                  "1234\n",
	})

	orig := blockBase
	blockBase = root
	defer func() { blockBase = orig }()

	hs := ReadStorageHealth()
	if len(hs) != 1 {
		t.Fatalf("got %d devices, want 1 (loop/zram must be skipped): %+v", len(hs), hs)
	}
	h := hs[0]

	if h.Kind != "eMMC" {
		t.Errorf("Kind = %q, want eMMC", h.Kind)
	}
	if h.Name != "BJTD4R" {
		t.Errorf("Name = %q", h.Name)
	}
	if h.SizeB != 62333952*512 {
		t.Errorf("SizeB = %d", h.SizeB)
	}
	if !h.HasLife || h.LifeA != 3 || h.LifeB != 2 {
		t.Errorf("life = %d/%d (has=%v), want 3/2", h.LifeA, h.LifeB, h.HasLife)
	}
	// The worst band wins, and the band's upper bound is the honest reading.
	pct, ok := h.WearPct()
	if !ok || pct != 30 {
		t.Errorf("WearPct() = %.0f (%v), want 30", pct, ok)
	}
	if h.EOLLabel() != "warning" {
		t.Errorf("EOLLabel() = %q, want warning", h.EOLLabel())
	}
	if h.Severity() != 1 {
		t.Errorf("Severity() = %d, want 1 for a pre-EOL warning", h.Severity())
	}
}

func TestStorageHealthSeverity(t *testing.T) {
	cases := []struct {
		name string
		h    StorageHealth
		want int
	}{
		{"fresh card", StorageHealth{LifeA: 1, PreEOL: 1}, 0},
		{"half worn", StorageHealth{LifeA: 5, PreEOL: 1}, 0},
		{"70% worn", StorageHealth{LifeA: 7, PreEOL: 1}, 1},
		{"pre-eol warning", StorageHealth{LifeA: 2, PreEOL: 2}, 1},
		{"90% worn", StorageHealth{LifeA: 9, PreEOL: 1}, 2},
		{"reserve exhausted", StorageHealth{LifeA: 11}, 2},
		{"pre-eol urgent", StorageHealth{LifeA: 1, PreEOL: 3}, 2},
		{"flipped read-only", StorageHealth{ReadOnly: true}, 2},
		{"no data at all", StorageHealth{}, 0},
	}
	for _, c := range cases {
		if got := c.h.Severity(); got != c.want {
			t.Errorf("%s: Severity() = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestStorageHealthWearPctRejectsNonsense(t *testing.T) {
	for _, band := range []int{0, -1, 12, 255} {
		if _, ok := (StorageHealth{LifeA: band}).WearPct(); ok {
			t.Errorf("band %d reported as a usable wear reading", band)
		}
	}
}

func TestReadStorageHealthPlainSDAndNVMe(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		// A plain SD card: identity only, no wear registers.
		"mmcblk0/size":          "31116288\n",
		"mmcblk0/device/type":   "SD\n",
		"mmcblk0/device/name":   "SC32G\n",
		"nvme0n1/size":          "1000215216\n",
		"nvme0n1/device/model":  "Samsung SSD 980\n",
		"nvme0n1/device/serial": "S1234\n",
	})

	orig := blockBase
	blockBase = root
	defer func() { blockBase = orig }()

	hs := ReadStorageHealth()
	if len(hs) != 2 {
		t.Fatalf("got %d devices, want 2", len(hs))
	}
	if hs[0].Kind != "SD" {
		t.Errorf("mmcblk0 Kind = %q, want SD", hs[0].Kind)
	}
	if hs[0].HasLife {
		t.Error("a plain SD card must not claim eMMC life-time data")
	}
	if _, ok := hs[0].WearPct(); ok {
		t.Error("a card with no wear registers reported a wear percentage")
	}
	if hs[1].Kind != "NVMe" || hs[1].Name != "Samsung SSD 980" {
		t.Errorf("nvme row = %+v", hs[1])
	}
}

func TestReadStorageHealthAbsent(t *testing.T) {
	orig := blockBase
	blockBase = filepath.Join(t.TempDir(), "nope")
	defer func() { blockBase = orig }()

	if hs := ReadStorageHealth(); hs != nil {
		t.Errorf("missing /sys/block yielded %+v, want nil", hs)
	}
}

func TestParseHexByte(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"0x02", 2, true},
		{"0x0B", 11, true},
		{" 3 ", 3, true},
		{"", 0, false},
		{"zz", 0, false},
	}
	for _, c := range cases {
		got, ok := parseHexByte(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseHexByte(%q) = %d,%v want %d,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}
