package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidPackages(t *testing.T) {
	ok, err := validPackages([]string{"libc6", "linux-image-6.12.0-1-amd64", "g++", "libstdc++6", "libc6"})
	if err != nil || !reflect.DeepEqual(ok, []string{"libc6", "linux-image-6.12.0-1-amd64", "g++", "libstdc++6"}) {
		t.Fatalf("got %v %v", ok, err)
	}
	for _, bad := range []string{"", "-oDpkg::Pre-Invoke=x", "Foo", "a b", "a;rm", "pkg:amd64", "../x", "$(id)", "a\nb", strings.Repeat("a", 129)} {
		if _, err := validPackages([]string{bad}); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if _, err := validPackages(nil); err == nil {
		t.Fatal("empty selection accepted")
	}
	many := make([]string, 501)
	for i := range many {
		many[i] = "p" + strings.Repeat("a", i%50) + string(rune('a'+i%26))
	}
	if _, err := validPackages(many); err == nil {
		t.Fatal("501 packages accepted")
	}
}

func TestParseUpgradable(t *testing.T) {
	out := `Listing... Done
base-files/stable 13.8 amd64 [upgradable from: 13.7]
libc6/stable-security 2.41-12+deb13u1 amd64 [upgradable from: 2.41-12]
openssl/noble-security,noble-updates 3.0.13-0ubuntu3.5 amd64 [upgradable from: 3.0.13-0ubuntu3.4]
WARNING: apt does not have a stable CLI interface.
garbage line
`
	got := parseUpgradable(out)
	if len(got) != 3 {
		t.Fatalf("got %d packages: %+v", len(got), got)
	}
	if got[0].Name != "base-files" || got[0].Candidate != "13.8" || got[0].Current != "13.7" || got[0].Security {
		t.Fatalf("base-files: %+v", got[0])
	}
	if !got[1].Security || got[1].Arch != "amd64" {
		t.Fatalf("libc6: %+v", got[1])
	}
	if !got[2].Security {
		t.Fatalf("openssl: %+v", got[2])
	}
}

func TestParseSimulation(t *testing.T) {
	out := `NOTE: This is only a simulation!
Inst libc6 [2.41-12] (2.41-12+deb13u1 Debian-Security:13/stable-security [amd64])
Inst libc-bin [2.41-12] (2.41-12+deb13u1 Debian-Security:13/stable-security [amd64])
Inst linux-image-6.12.48+deb13-amd64 (6.12.48-1 Debian:13.8/stable [amd64])
Remv oldpkg [1.0]
Conf libc6 (2.41-12+deb13u1 Debian-Security:13/stable-security [amd64])
`
	s := parseSimulation(out)
	if !reflect.DeepEqual(s.Upgrade, []string{"libc6", "libc-bin"}) {
		t.Fatalf("upgrade %v", s.Upgrade)
	}
	if !reflect.DeepEqual(s.Install, []string{"linux-image-6.12.48+deb13-amd64"}) || !s.Kernel {
		t.Fatalf("install %v kernel %v", s.Install, s.Kernel)
	}
	if !reflect.DeepEqual(s.Remove, []string{"oldpkg"}) {
		t.Fatalf("remove %v", s.Remove)
	}
}

func TestAptErrors(t *testing.T) {
	cases := map[string]string{
		"Err:4 https://enterprise.proxmox.com/debian/pve trixie InRelease\n  401  Unauthorized [IP: 1.2.3.4 443]": "auth",
		"W: GPG error: http://x stable InRelease: NO_PUBKEY ABCDEF":                                               "key",
		"Err:1 http://deb.debian.org trixie InRelease\n  Temporary failure resolving 'deb.debian.org'":            "network",
		"E: The repository 'http://x foo Release' does not have a Release file.":                                  "release",
		"E: Could not get lock /var/lib/apt/lists/lock. It is held by process 123 (apt-get)":                      "lock",
	}
	for out, want := range cases {
		lines, reason := aptErrors(out)
		if reason != want || len(lines) == 0 {
			t.Fatalf("%q: reason %q lines %v, want %q", out, reason, lines, want)
		}
	}
	if lines, _ := aptErrors("Err:10 file:/opt/repo ./ Translation-en\n  File not found - /opt/repo/./en.xz"); len(lines) != 0 {
		t.Fatalf("missing translations reported: %v", lines)
	}
	if lines, reason := aptErrors("Hit:1 http://deb.debian.org trixie InRelease\nReading package lists..."); len(lines) != 0 || reason != "" {
		t.Fatalf("clean output gave %v %q", lines, reason)
	}
}

func TestAutoUpdatesDropIn(t *testing.T) {
	b := autoUpdatesDropIn()
	if !strings.Contains(b, `APT::Periodic::Unattended-Upgrade "1";`) || !strings.Contains(b, `APT::Periodic::Update-Package-Lists "1";`) {
		t.Fatalf("drop-in: %s", b)
	}
}
