package probe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePSI(t *testing.T) {
	r := parsePSI("some avg10=1.25 avg60=0.50 avg300=0.10 total=123\nfull avg10=0.75 avg60=0.25 avg300=0.05 total=45\n")
	if !r.HasSome || !r.HasFull {
		t.Fatalf("HasSome=%v HasFull=%v", r.HasSome, r.HasFull)
	}
	if r.SomeAvg10 != 1.25 || r.SomeAvg60 != 0.50 {
		t.Errorf("some = %v/%v", r.SomeAvg10, r.SomeAvg60)
	}
	if r.FullAvg10 != 0.75 || r.FullAvg60 != 0.25 {
		t.Errorf("full = %v/%v", r.FullAvg10, r.FullAvg60)
	}

	if r := parsePSI("garbage\n"); r.HasSome || r.HasFull {
		t.Error("garbage input should parse to nothing")
	}
}

func TestReadPSI(t *testing.T) {
	base := t.TempDir()
	old := psiBase
	psiBase = base
	defer func() { psiBase = old }()

	// CPU exposes only "some"; memory has both lines.
	if err := os.WriteFile(filepath.Join(base, "cpu"),
		[]byte("some avg10=3.00 avg60=1.00 avg300=0.20 total=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "memory"),
		[]byte("some avg10=0.10 avg60=0.00 avg300=0.00 total=1\nfull avg10=0.05 avg60=0.00 avg300=0.00 total=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap := ReadPSI()
	if !snap.Present {
		t.Fatal("Present should be true when pressure files parse")
	}
	if snap.CPU.SomeAvg10 != 3.0 || snap.CPU.HasFull {
		t.Errorf("cpu = %+v", snap.CPU)
	}
	if snap.Mem.FullAvg10 != 0.05 || !snap.Mem.HasFull {
		t.Errorf("mem = %+v", snap.Mem)
	}
	if snap.IO.HasSome {
		t.Error("io file absent — should stay empty")
	}
}

func TestReadPSIAbsent(t *testing.T) {
	old := psiBase
	psiBase = filepath.Join(t.TempDir(), "nope")
	defer func() { psiBase = old }()

	if snap := ReadPSI(); snap.Present {
		t.Error("Present should be false without /proc/pressure")
	}
}
