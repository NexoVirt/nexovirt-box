package box

import (
	"strings"
	"testing"
	"time"
)

func TestParseUPS(t *testing.T) {
	u := parseUPS("eaton", "battery.charge: 87\nbattery.runtime: 1520\ndevice.model: Ellipse PRO\nups.load: 23\nups.status: OB DISCHRG\n")
	if u.Charge != 87 || u.Runtime != 1520 || u.Load != 23 || u.Status != "OB DISCHRG" || u.Model != "Ellipse PRO" {
		t.Fatalf("%+v", u)
	}
	if u := parseUPS("x", "ups.status: OL\n"); u.Charge != -1 || u.Load != -1 {
		t.Fatalf("missing values must be -1: %+v", u)
	}
}

func TestHealthProblems(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	hot := int64(63)
	disks := []diskInfo{
		{Path: "/dev/sda", Model: "WD", Smart: &smartSummary{Verdict: "failing"}},
		{Path: "/dev/sdb", Model: "ST", Smart: &smartSummary{Verdict: "good", Temperature: &hot}},
		{Path: "/dev/vda", Virtual: true},
	}
	temps := []tempReading{{Sensor: "coretemp_core0", Celsius: 96, Critical: 100}, {Sensor: "acpitz", Celsius: 40, Critical: 100}}
	ups := []upsInfo{{Name: "eaton", Status: "OB LB", Charge: 8}}
	backups := []backupAge{
		{App: "gitea", LastOK: now.Add(-10 * 24 * time.Hour).Unix(), Scheduled: true},
		{App: "memos", LastOK: now.Add(-time.Hour).Unix(), Scheduled: true},
		{App: "box", Scheduled: true},
		{App: "old", LastOK: 1, Scheduled: false},
	}
	var keys []string
	for _, p := range healthProblems(disks, temps, ups, backups, now) {
		keys = append(keys, p.Key+"="+p.Level)
	}
	want := "disk:/dev/sda=error disktemp:/dev/sdb=warning temp:coretemp_core0=warning ups:eaton=error backup:gitea=warning backup:box=warning"
	if strings.Join(keys, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(keys, " "), want)
	}
}
