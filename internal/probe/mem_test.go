package probe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadOOMKills(t *testing.T) {
	old := vmstatPath
	defer func() { vmstatPath = old }()
	dir := t.TempDir()

	vmstatPath = filepath.Join(dir, "vmstat")
	if err := os.WriteFile(vmstatPath, []byte("nr_free_pages 1234\noom_kill 7\npgfault 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, ok := ReadOOMKills(); !ok || n != 7 {
		t.Errorf("ReadOOMKills = %d, %v; want 7, true", n, ok)
	}
	if c := CheckOOMKills(); c.Status != CapPresent {
		t.Errorf("CheckOOMKills = %+v, want present", c)
	}

	// A kernel before 4.13 has vmstat but no counter.
	if err := os.WriteFile(vmstatPath, []byte("nr_free_pages 1234\noom_kill_x 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadOOMKills(); ok {
		t.Error("a vmstat without oom_kill reported a counter")
	}
	if c := CheckOOMKills(); c.Status != CapAbsent || c.Detail == "" {
		t.Errorf("CheckOOMKills without the line = %+v, want absent with a reason", c)
	}

	vmstatPath = filepath.Join(dir, "missing")
	if _, ok := ReadOOMKills(); ok {
		t.Error("a missing vmstat reported a counter")
	}
}
