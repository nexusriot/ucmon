package ui

import (
	"strings"
	"testing"

	"github.com/nexusriot/ucmon/internal/probe"
)

// Governor and backlight writes need root or a udev rule, so every refusal
// below must surface as a status line and leave sampling running — never abort
// the tick or let the UI claim a change that did not happen.
func TestCycleGovernorNoPolicies(t *testing.T) {
	m := testModel()
	if cmd := m.cycleGovernor(); cmd != nil {
		t.Error("a host without cpufreq should dispatch nothing")
	}
	if n := m.notice.get(); !strings.Contains(n, "no cpufreq") {
		t.Errorf("notice = %q", n)
	}
}

func TestCycleGovernorSingleGovernor(t *testing.T) {
	m := testModel()
	m.cpuSnap.Freqs = []probe.PolicyFreq{
		{Policy: "policy0", Governor: "performance", Available: []string{"performance"}},
	}
	if cmd := m.cycleGovernor(); cmd != nil {
		t.Error("nothing to cycle to should dispatch nothing")
	}
	if n := m.notice.get(); !strings.Contains(n, "no alternative governors") {
		t.Errorf("notice = %q", n)
	}
}

// A policy directory that cannot exist makes the sysfs write fail, which is
// the same code path a non-root user hits on a real board.
func TestCycleGovernorWriteFailureIsReported(t *testing.T) {
	m := testModel()
	m.cpuSnap.Freqs = []probe.PolicyFreq{{
		Policy:    "policy-ucmon-test-does-not-exist",
		Governor:  "ondemand",
		Available: []string{"ondemand", "performance"},
	}}

	if cmd := m.cycleGovernor(); cmd != nil {
		t.Error("a failed write should not dispatch a refresh")
	}
	if n := m.notice.get(); !strings.Contains(n, "governor:") {
		t.Errorf("notice = %q, want the write error", n)
	}
}

func TestAdjustBacklightAbsentDevice(t *testing.T) {
	m := testModel()
	m.backlight = probe.BacklightInfo{Present: false}

	if cmd := m.adjustBacklight(10); cmd != nil {
		t.Error("no backlight should dispatch nothing")
	}
	if n := m.sysNotice.get(); !strings.Contains(n, "no backlight device") {
		t.Errorf("sysNotice = %q", n)
	}
}

func TestAdjustBacklightWriteFailureIsReported(t *testing.T) {
	m := testModel()
	m.backlight = probe.BacklightInfo{
		Present: true, Device: "ucmon-test-no-such-panel",
		Brightness: 128, Max: 255, Percent: 50, Writable: false,
	}

	if cmd := m.adjustBacklight(10); cmd != nil {
		t.Error("a failed write should not dispatch a refresh")
	}
	if n := m.sysNotice.get(); !strings.Contains(n, "brightness:") {
		t.Errorf("sysNotice = %q, want the write error", n)
	}
	if m.backlight.Percent != 50 {
		t.Errorf("Percent moved to %d despite the write failing", m.backlight.Percent)
	}
}

// The +/- keys only act on the GPU tab, and a refusal must not be mistaken for
// a change anywhere.
func TestBacklightKeysAreTabScoped(t *testing.T) {
	for _, tb := range []tab{tabCPU, tabMem, tabProc, tabDisk, tabNet, tabPower, tabAlerts} {
		m := testModel()
		m.activeTab = tb
		m.backlight = probe.BacklightInfo{Present: true, Device: "x", Max: 255, Percent: 50}

		m, _ = press(t, m, key('+'))
		if n := m.sysNotice.get(); n != "" {
			t.Errorf("tab %d: '+' touched the backlight (%q)", tb, n)
		}
	}

	m := testModel()
	m.activeTab = tabGPU
	m.backlight = probe.BacklightInfo{Present: false}
	m, _ = press(t, m, key('+'))
	if n := m.sysNotice.get(); n == "" {
		t.Error("'+' on the GPU tab did not reach adjustBacklight")
	}
}

// g only cycles the governor on the CPU tab.
func TestGovernorKeyIsTabScoped(t *testing.T) {
	for _, tb := range []tab{tabMem, tabProc, tabDisk, tabNet, tabPower, tabGPU, tabAlerts} {
		m := testModel()
		m.activeTab = tb
		m, _ = press(t, m, key('g'))
		if n := m.notice.get(); n != "" {
			t.Errorf("tab %d: 'g' cycled the governor (%q)", tb, n)
		}
	}

	m := testModel()
	m.activeTab = tabCPU
	m, _ = press(t, m, key('g'))
	if n := m.notice.get(); !strings.Contains(n, "cpufreq") {
		t.Errorf("'g' on the CPU tab did not reach cycleGovernor (%q)", n)
	}
}
