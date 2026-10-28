package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShareNameAndUserValidation(t *testing.T) {
	for _, ok := range []string{"media", "Backup_2", "team.docs"} {
		if err := validShareName(ok); err != nil {
			t.Fatalf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "global", "GLOBAL", "homes", "printers", "print$", "ipc$", "a]b", "a b", "x\ny", strings.Repeat("a", 33), "-x"} {
		if validShareName(bad) == nil {
			t.Fatalf("share name %q accepted", bad)
		}
	}
	for _, bad := range []string{"", "Root", "a b", "-x", "x;y", strings.Repeat("a", 33)} {
		if validSambaUser(bad) == nil {
			t.Fatalf("user %q accepted", bad)
		}
	}
	if validSambaPassword("short") == nil || validSambaPassword("good password\n") == nil || validSambaPassword("good password") != nil {
		t.Fatal("password rules")
	}
}

func TestRenderSamba(t *testing.T) {
	shares := []sambaShare{
		{ID: "a1", Name: "media", Path: "/srv/media", ReadOnly: true, Users: []string{"bob", "alice"}, Enabled: true, Comment: "Films"},
		{ID: "b2", Name: "off", Path: "/srv/off", Users: []string{"bob"}, Enabled: false},
		{ID: "c3", Name: "pub", Path: "/srv/pub", ReadOnly: true, Guest: true, Enabled: true},
	}
	owner := func(string) (string, string) { return "bob", "users" }
	out := renderSamba(shares, owner)
	for _, want := range []string{"[media]", "path = /srv/media", "read only = yes", "valid users = alice bob", "force user = bob",
		"force group = users", "create mask = 0664", "directory mask = 0775", "wide links = no", "guest ok = no", "comment = Films", "[pub]", "guest ok = yes", "map to guest = Bad User"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "[off]") {
		t.Fatalf("disabled share rendered:\n%s", out)
	}
	if !strings.HasPrefix(out, "#") {
		t.Fatal("no header")
	}
	if out := renderSamba(nil, owner); strings.Contains(out, "[") {
		t.Fatalf("empty render has sections: %s", out)
	}
}

func TestSambaShareChecks(t *testing.T) {
	base := sambaShare{Name: "x", Path: "/srv/x", Users: []string{"bob"}}
	users := []string{"bob"}
	if err := checkSambaShare(base, users); err != nil {
		t.Fatal(err)
	}
	bad := []sambaShare{
		{Name: "x", Path: "/srv/x"}, // nobody may use it
		{Name: "x", Path: "/srv/x", Users: []string{"eve"}},
		{Name: "x", Path: "/srv/x", Guest: true}, // guest needs read only
		{Name: "x", Path: "/srv/x", Users: []string{"bob"}, Comment: "a\nb"},
		{Name: "x", Path: "/srv/x\n[evil]", Users: []string{"bob"}},
	}
	for _, s := range bad {
		if checkSambaShare(s, users) == nil {
			t.Fatalf("%+v accepted", s)
		}
	}
}

func TestAddInclude(t *testing.T) {
	conf := "[global]\n   workgroup = WORKGROUP\n\n[homes]\n   browseable = no\n"
	out, changed := addInclude(conf)
	if !changed || !strings.HasPrefix(out, conf) || !strings.Contains(out, "include = /etc/samba/nexovirt-box.conf") {
		t.Fatalf("%v\n%s", changed, out)
	}
	if again, changed := addInclude(out); changed || again != out {
		t.Fatal("include added twice")
	}
	if _, changed := addInclude("[global]\ninclude = /etc/samba/nexovirt-box.conf"); changed {
		t.Fatal("existing include without newline not seen")
	}
}

func TestNFSExports(t *testing.T) {
	cl, err := validNFSClients([]string{"192.168.1.0/24", "10.0.0.5", "fd00::/64"})
	if err != nil || strings.Join(cl, ",") != "192.168.1.0/24,10.0.0.5/32,fd00::/64" {
		t.Fatalf("%v %v", cl, err)
	}
	for _, bad := range [][]string{nil, {""}, {"0.0.0.0/0"}, {"10.0.0.0/4"}, {"::/0"}, {"*"}, {"host.example"}, {"1.2.3.4(rw,no_root_squash)"}} {
		if _, err := validNFSClients(bad); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
	for _, bad := range []string{"/srv/a b", "/srv/x\"y", "/srv/x(rw)", "relative"} {
		if validNFSPath(bad) == nil {
			t.Fatalf("path %q accepted", bad)
		}
	}
	out := renderExports([]nfsExport{
		{ID: "a", Path: "/srv/a", Clients: []string{"192.168.1.0/24", "10.0.0.5/32"}, ReadOnly: true, Enabled: true},
		{ID: "b", Path: "/srv/b", Clients: []string{"10.0.0.0/8"}, Enabled: false},
	})
	if !strings.Contains(out, "/srv/a 192.168.1.0/24(ro,sync,no_subtree_check,root_squash) 10.0.0.5/32(ro,sync,no_subtree_check,root_squash)") {
		t.Fatalf("render:\n%s", out)
	}
	if strings.Contains(out, "/srv/b") || strings.Contains(out, "no_root_squash") {
		t.Fatalf("render:\n%s", out)
	}
}

func TestParseClients(t *testing.T) {
	smb := `{"tcons":{"1":{"service":"media","machine":"192.168.1.20"},"2":{"service":"media","machine":"192.168.1.21"},"3":{"service":"IPC$","machine":"x"}},
	 "sessions":{"9":{"username":"bob","remote_machine":"192.168.1.20"}}}`
	m := parseSmbstatus([]byte(smb))
	if len(m["media"]) != 2 || len(m["IPC$"]) != 0 {
		t.Fatalf("%v", m)
	}
	ss := "0      0      10.0.0.1:2049      10.0.0.7:812\n0 0 [::ffff:10.0.0.1]:2049 [::ffff:10.0.0.8]:700\n"
	if got := parseNFSPeers(ss); strings.Join(got, ",") != "10.0.0.7,10.0.0.8" {
		t.Fatalf("%v", got)
	}
}

func TestHandoverPathRules(t *testing.T) {
	base := "/var/lib/nexovirt-box"
	for _, bad := range []string{"/", "/etc", "/etc/samba", "/usr/share", "/var/lib/nexovirt-box", "/var/lib/nexovirt-box/apps/x", "/var/lib/docker/volumes", "/root/x"} {
		if handoverPathOK(bad, []string{"/srv", "/var/lib/nexovirt-box/apps"}, base) == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	if handoverPathOK("/srv", []string{"/srv"}, base) == nil {
		t.Fatal("a top folder itself accepted")
	}
	if err := handoverPathOK("/srv/media", []string{"/srv"}, base); err != nil {
		t.Fatal(err)
	}
}

// The walk changes owners below the folder only and never follows a symlink: the link itself
// changes owner (lchown), its target outside never does.
func TestHandoverWalkSkipsSymlinkTargets(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600)
	os.MkdirAll(filepath.Join(root, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(root, "a", "f.txt"), []byte("x"), 0o644)
	os.Symlink(outside, filepath.Join(root, "a", "out"))
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link-file"))
	var seen []string
	n, err := handoverWalk(root, func(p string) error { seen = append(seen, p); return nil })
	if err != nil || n != 6 { // root, a, a/b, a/f.txt, a/out, link-file
		t.Fatalf("n=%d err=%v seen=%v", n, err, seen)
	}
	for _, p := range seen {
		if strings.HasPrefix(p, outside) {
			t.Fatalf("walked outside: %s", p)
		}
	}
}

func TestReadWriteNeedsOwnerAmongUsers(t *testing.T) {
	dir := t.TempDir()
	me, _ := folderOwner(dir)
	if rwOwnerOK(sambaShare{Path: dir, Users: []string{"someone-else"}}) == nil {
		t.Fatal("read-write share on a folder owned by nobody in the list accepted")
	}
	if err := rwOwnerOK(sambaShare{Path: dir, Users: []string{me}}); err != nil {
		t.Fatal(err)
	}
	if err := rwOwnerOK(sambaShare{Path: dir, ReadOnly: true, Users: []string{"someone-else"}}); err != nil {
		t.Fatal(err)
	}
}
