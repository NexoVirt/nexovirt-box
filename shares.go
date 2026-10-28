package main

// Network shares, root side. Samba: Box writes ONLY /etc/samba/nexovirt-box.conf and adds one
// include line to smb.conf once (the rest of smb.conf is never rewritten); its users are Linux
// system users without a shell or home that Box created itself. NFS: Box writes ONLY
// /etc/exports.d/nexovirt-box.exports, always with root_squash and an explicit client list.
// Every share folder must be inside the file explorer's allowed folders. A config that testparm
// or exportfs refuses is rolled back to the previous file.

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var (
	smbConf     = "/etc/samba/smb.conf"
	smbBoxConf  = "/etc/samba/nexovirt-box.conf"
	nfsBoxFile  = "/etc/exports.d/nexovirt-box.exports"
	sharesMu    sync.Mutex
	includeLine = "include = " + smbBoxConf
)

type sambaShare struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	ReadOnly bool     `json:"read_only"`
	Guest    bool     `json:"guest"`
	Users    []string `json:"users"`
	Enabled  bool     `json:"enabled"`
	Comment  string   `json:"comment"`
}

type nfsExport struct {
	ID       string   `json:"id"`
	Path     string   `json:"path"`
	Clients  []string `json:"clients"`
	ReadOnly bool     `json:"read_only"`
	Enabled  bool     `json:"enabled"`
	Comment  string   `json:"comment"`
}

type sharesState struct {
	Samba      []sambaShare `json:"samba"`
	SambaUsers []string     `json:"samba_users"`
	NFS        []nfsExport  `json:"nfs"`
}

func loadShares() sharesState {
	var s sharesState
	readJSONFile(fwPath("shares.json"), &s)
	return s
}

// ---- validation ----

var (
	shareNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)
	sambaUserRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	nfsPathRe   = regexp.MustCompile(`^/[A-Za-z0-9._/+-]*$`)
	reserved    = map[string]bool{"global": true, "homes": true, "printers": true, "print$": true, "ipc$": true}
)

func validShareName(n string) error {
	if !shareNameRe.MatchString(n) || reserved[strings.ToLower(n)] {
		return uerr("Use 1 to 32 letters, digits, dots, dashes or underscores for the share name (not global, homes or printers).")
	}
	return nil
}

func validSambaUser(n string) error {
	if !sambaUserRe.MatchString(n) {
		return uerr("Use 1 to 32 lowercase letters, digits, dashes or underscores for the user name, starting with a letter.")
	}
	return nil
}

func validSambaPassword(p string) error {
	if len(p) < 8 || len(p) > 128 || strings.ContainsAny(p, "\n\r\x00") {
		return uerr("The password needs 8 to 128 characters and no line breaks.")
	}
	return nil
}

func checkSambaShare(s sambaShare, users []string) error {
	if err := validShareName(s.Name); err != nil {
		return err
	}
	if !filepath.IsAbs(s.Path) || strings.ContainsAny(s.Path, "\n\r\x00") {
		return uerr("The folder is not valid.")
	}
	if !commentRe.MatchString(s.Comment) {
		return uerr("The description may only use letters, digits and simple punctuation (60 at most).")
	}
	if s.Guest && !s.ReadOnly {
		return uerr("Guest access is only possible for read-only shares.")
	}
	if !s.Guest && len(s.Users) == 0 {
		return uerr("Choose at least one user who may use this share.")
	}
	known := map[string]bool{}
	for _, u := range users {
		known[u] = true
	}
	for _, u := range s.Users {
		if !known[u] {
			return uerr("%s is not a share user. Add the user first.", u)
		}
	}
	return nil
}

func validNFSPath(p string) error {
	if !nfsPathRe.MatchString(p) {
		return uerr("NFS folders may only contain letters, digits and . _ - + / (no spaces).")
	}
	return nil
}

// validNFSClients requires an explicit list of addresses or networks; "everyone" is refused.
func validNFSClients(list []string) ([]string, error) {
	out := []string{}
	for _, c := range list {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if ip := net.ParseIP(c); ip != nil {
			if ip.To4() != nil {
				c = ip.String() + "/32"
			} else {
				c = ip.String() + "/128"
			}
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, uerr("%s is not an IP address or network like 192.168.1.0/24.", c)
		}
		ones, bits := n.Mask.Size()
		if (bits == 32 && ones < 8) || (bits == 128 && ones < 16) {
			return nil, uerr("%s is too wide. Allow only your own networks.", c)
		}
		out = append(out, n.String())
	}
	if len(out) == 0 {
		return nil, uerr("Enter at least one client address or network.")
	}
	if len(out) > 50 {
		return nil, uerr("Enter at most 50 clients.")
	}
	return out, nil
}

// ---- rendering ----

func renderSamba(shares []sambaShare, owner func(string) (string, string)) string {
	var b strings.Builder
	b.WriteString("# Managed by NexoVirt Box (Shares). Changes here are overwritten.\n")
	list := append([]sambaShare(nil), shares...)
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	for _, s := range list {
		if s.Enabled && s.Guest {
			b.WriteString("\n[global]\n   map to guest = Bad User\n")
			break
		}
	}
	for _, s := range list {
		if !s.Enabled {
			continue
		}
		u, g := owner(s.Path)
		fmt.Fprintf(&b, "\n[%s]\n   path = %s\n", s.Name, s.Path)
		if s.Comment != "" {
			fmt.Fprintf(&b, "   comment = %s\n", s.Comment)
		}
		fmt.Fprintf(&b, "   read only = %s\n   browseable = yes\n   wide links = no\n", yesNo(s.ReadOnly))
		if s.Guest {
			b.WriteString("   guest ok = yes\n   guest only = yes\n")
		} else {
			users := append([]string(nil), s.Users...)
			sort.Strings(users)
			fmt.Fprintf(&b, "   guest ok = no\n   valid users = %s\n", strings.Join(users, " "))
		}
		// Files are read and written as the folder's owner, so the share keeps its permissions.
		fmt.Fprintf(&b, "   force user = %s\n   force group = %s\n", u, g)
	}
	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func renderExports(list []nfsExport) string {
	var b strings.Builder
	b.WriteString("# Managed by NexoVirt Box (Shares). Changes here are overwritten.\n")
	for _, e := range list {
		if !e.Enabled {
			continue
		}
		opt := "rw"
		if e.ReadOnly {
			opt = "ro"
		}
		b.WriteString(e.Path)
		for _, c := range e.Clients {
			fmt.Fprintf(&b, " %s(%s,sync,no_subtree_check,root_squash)", c, opt)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// addInclude appends the include line to smb.conf once; everything else stays as it is.
func addInclude(conf string) (string, bool) {
	for _, l := range strings.Split(conf, "\n") {
		if strings.ReplaceAll(strings.TrimSpace(l), " ", "") == strings.ReplaceAll(includeLine, " ", "") {
			return conf, false
		}
	}
	if conf != "" && !strings.HasSuffix(conf, "\n") {
		conf += "\n"
	}
	return conf + "\n# Shares managed by NexoVirt Box\n" + includeLine + "\n", true
}

func folderOwner(p string) (string, string) {
	u, g := "root", "root"
	st, err := os.Stat(p)
	if err != nil {
		return u, g
	}
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		if x, err := user.LookupId(strconv.Itoa(int(s.Uid))); err == nil {
			u = x.Username
		}
		if x, err := user.LookupGroupId(strconv.Itoa(int(s.Gid))); err == nil {
			g = x.Name
		}
	}
	return u, g
}

// ---- status ----

func parseSmbstatus(b []byte) map[string][]string {
	var j struct {
		Tcons map[string]struct{ Service, Machine string } `json:"tcons"`
	}
	out := map[string][]string{}
	if json.Unmarshal(b, &j) != nil {
		return out
	}
	for _, t := range j.Tcons {
		if strings.EqualFold(t.Service, "IPC$") {
			continue
		}
		out[t.Service] = append(out[t.Service], t.Machine)
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// parseNFSPeers reads `ss -Htn state established ( sport = :2049 )`: the peer is the last column.
func parseNFSPeers(out string) []string {
	seen := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		host, _, err := net.SplitHostPort(f[len(f)-1])
		if err != nil {
			continue
		}
		host = strings.TrimPrefix(host, "::ffff:")
		if net.ParseIP(host) != nil {
			seen[host] = true
		}
	}
	return sortedKeys(seen)
}

func serviceActive(unit string) bool {
	return exec.Command("systemctl", "is-active", "--quiet", unit).Run() == nil
}

func sambaInstalled() bool { _, err := exec.LookPath("smbd"); return err == nil }
func nfsInstalled() bool {
	_, err := exec.LookPath("exportfs")
	return err == nil && exists("/usr/sbin/rpc.nfsd")
}

func (h *helperServer) sharesStatus(req helperRequest, logf func(string)) (any, error) {
	sharesMu.Lock()
	st := loadShares()
	sharesMu.Unlock()
	clients := map[string][]string{}
	if sambaInstalled() {
		if out, err := exec.Command("smbstatus", "--json").Output(); err == nil {
			clients = parseSmbstatus(out)
		}
	}
	var peers []string
	if out, err := exec.Command("ss", "-Htn", "state", "established", "( sport = :2049 )").Output(); err == nil {
		peers = parseNFSPeers(string(out))
	}
	if st.Samba == nil {
		st.Samba = []sambaShare{}
	}
	if st.SambaUsers == nil {
		st.SambaUsers = []string{}
	}
	if st.NFS == nil {
		st.NFS = []nfsExport{}
	}
	return map[string]any{
		"samba": map[string]any{"installed": sambaInstalled(), "active": serviceActive("smbd"), "shares": st.Samba,
			"users": st.SambaUsers, "clients": clients},
		"nfs": map[string]any{"installed": nfsInstalled(), "active": serviceActive("nfs-server"), "exports": st.NFS,
			"clients": peers},
	}, nil
}

// ---- samba ----

func (h *helperServer) applySamba(st sharesState) error {
	if !sambaInstalled() {
		return uerr("Install Samba first.")
	}
	prev, prevErr := os.ReadFile(smbBoxConf)
	if err := writeFileAtomic(smbBoxConf, []byte(renderSamba(st.Samba, folderOwner)), 0o644); err != nil {
		return err
	}
	restore := func() {
		if prevErr == nil {
			writeFileAtomic(smbBoxConf, prev, 0o644)
		} else {
			os.Remove(smbBoxConf)
		}
	}
	conf, err := os.ReadFile(smbConf)
	if err != nil {
		restore()
		return uerr("%s could not be read.", smbConf)
	}
	if out, changed := addInclude(string(conf)); changed {
		mode := os.FileMode(0o644)
		if fi, err := os.Stat(smbConf); err == nil {
			mode = fi.Mode().Perm()
		}
		if err := writeFileAtomic(smbConf, []byte(out), mode); err != nil {
			restore()
			return err
		}
	}
	if out, err := exec.Command("testparm", "-s", "--suppress-prompt").CombinedOutput(); err != nil {
		restore()
		return uerr("Samba refused the new settings, so the previous ones are back: %s", strings.TrimSpace(lastLines(string(out), 2)))
	}
	if serviceActive("smbd") {
		exec.Command("smbcontrol", "smbd", "reload-config").Run()
	} else if hasEnabled(st) {
		if out, err := exec.Command("systemctl", "enable", "--now", "smbd").CombinedOutput(); err != nil {
			return uerr("The share is saved, but Samba did not start: %s", strings.TrimSpace(lastLines(string(out), 1)))
		}
	}
	return nil
}

func hasEnabled(st sharesState) bool {
	for _, s := range st.Samba {
		if s.Enabled {
			return true
		}
	}
	return false
}

func (h *helperServer) sambaShareSave(req helperRequest, logf func(string)) (any, error) {
	if req.Share == nil {
		return nil, uerr("The share is missing.")
	}
	s := *req.Share
	s.Name, s.Comment = strings.TrimSpace(s.Name), strings.TrimSpace(s.Comment)
	real, _, err := h.resolve(s.Path, true)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return nil, uerr("Choose a folder, not a file.")
	}
	s.Path = real
	sharesMu.Lock()
	defer sharesMu.Unlock()
	st := loadShares()
	if err := checkSambaShare(s, st.SambaUsers); err != nil {
		return nil, err
	}
	if s.Guest {
		s.Users = []string{}
	}
	idx := -1
	for i, o := range st.Samba {
		if o.ID == s.ID && s.ID != "" {
			idx = i
		} else if strings.EqualFold(o.Name, s.Name) {
			return nil, uerr("A share called %s already exists.", o.Name)
		}
	}
	if idx < 0 {
		s.ID = randomHex8()
		st.Samba = append(st.Samba, s)
	} else {
		st.Samba[idx] = s
	}
	if err := h.applySamba(st); err != nil {
		return nil, err
	}
	return s, writeJSONFile(fwPath("shares.json"), st)
}

func (h *helperServer) sambaShareDelete(req helperRequest, logf func(string)) (any, error) {
	sharesMu.Lock()
	defer sharesMu.Unlock()
	st := loadShares()
	out := st.Samba[:0]
	found := false
	for _, s := range st.Samba {
		if s.ID == req.ID {
			found = true
			continue
		}
		out = append(out, s)
	}
	if !found {
		return nil, uerr("This share no longer exists.")
	}
	st.Samba = out
	if err := h.applySamba(st); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, writeJSONFile(fwPath("shares.json"), st)
}

// sambaUserSave adds a share user (a Linux system user without shell or home, plus its Samba
// password) or sets a new password for one Box created.
func (h *helperServer) sambaUserSave(req helperRequest, logf func(string)) (any, error) {
	if err := validSambaUser(req.Name); err != nil {
		return nil, err
	}
	if err := validSambaPassword(req.Password); err != nil {
		return nil, err
	}
	if !sambaInstalled() {
		return nil, uerr("Install Samba first.")
	}
	sharesMu.Lock()
	defer sharesMu.Unlock()
	st := loadShares()
	mine := false
	for _, u := range st.SambaUsers {
		mine = mine || u == req.Name
	}
	created := false
	if !mine {
		if _, err := user.Lookup(req.Name); err == nil {
			return nil, uerr("A user called %s already exists on this server. Choose another name.", req.Name)
		}
		out, err := exec.Command("useradd", "--system", "--no-create-home", "--home-dir", "/nonexistent",
			"--shell", "/usr/sbin/nologin", "--comment", "NexoVirt Box share user", "--", req.Name).CombinedOutput()
		if err != nil {
			return nil, uerr("The user could not be created: %s", strings.TrimSpace(string(out)))
		}
		created = true
	}
	cmd := exec.Command("smbpasswd", "-a", "-s", req.Name)
	cmd.Stdin = strings.NewReader(req.Password + "\n" + req.Password + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		if created {
			exec.Command("userdel", "--", req.Name).Run()
		}
		return nil, uerr("Samba did not accept the user: %s", strings.TrimSpace(lastLines(string(out), 1)))
	}
	exec.Command("smbpasswd", "-e", req.Name).Run()
	if !mine {
		st.SambaUsers = append(st.SambaUsers, req.Name)
		sort.Strings(st.SambaUsers)
		if err := writeJSONFile(fwPath("shares.json"), st); err != nil {
			return nil, err
		}
	}
	return map[string]any{"users": st.SambaUsers}, nil
}

func (h *helperServer) sambaUserDelete(req helperRequest, logf func(string)) (any, error) {
	if err := validSambaUser(req.Name); err != nil {
		return nil, err
	}
	sharesMu.Lock()
	defer sharesMu.Unlock()
	st := loadShares()
	idx := -1
	for i, u := range st.SambaUsers {
		if u == req.Name {
			idx = i
		}
	}
	if idx < 0 {
		return nil, uerr("Box did not create this user, so it does not delete it.")
	}
	var using []string
	for _, s := range st.Samba {
		for _, u := range s.Users {
			if u == req.Name {
				using = append(using, s.Name)
			}
		}
	}
	if len(using) > 0 {
		return nil, uerr("%s still uses these shares: %s. Remove the user there first.", req.Name, strings.Join(using, ", "))
	}
	exec.Command("smbpasswd", "-x", req.Name).Run()
	if out, err := exec.Command("userdel", "--", req.Name).CombinedOutput(); err != nil && !strings.Contains(string(out), "does not exist") {
		return nil, uerr("The user could not be removed: %s", strings.TrimSpace(string(out)))
	}
	st.SambaUsers = append(st.SambaUsers[:idx], st.SambaUsers[idx+1:]...)
	return map[string]any{"users": st.SambaUsers}, writeJSONFile(fwPath("shares.json"), st)
}

// ---- nfs ----

func (h *helperServer) applyNFS(st sharesState) error {
	if !nfsInstalled() {
		return uerr("Install the NFS server first.")
	}
	if err := os.MkdirAll(filepath.Dir(nfsBoxFile), 0o755); err != nil {
		return err
	}
	prev, prevErr := os.ReadFile(nfsBoxFile)
	if err := writeFileAtomic(nfsBoxFile, []byte(renderExports(st.NFS)), 0o644); err != nil {
		return err
	}
	enabled := false
	for _, e := range st.NFS {
		enabled = enabled || e.Enabled
	}
	if enabled && !serviceActive("nfs-server") {
		exec.Command("systemctl", "enable", "--now", "nfs-server").Run()
	}
	if out, err := exec.Command("exportfs", "-ra").CombinedOutput(); err != nil || strings.Contains(string(out), "exportfs: ") && strings.Contains(string(out), "error") {
		if prevErr == nil {
			writeFileAtomic(nfsBoxFile, prev, 0o644)
		} else {
			os.Remove(nfsBoxFile)
		}
		exec.Command("exportfs", "-ra").Run()
		return uerr("The NFS server refused the export, so the previous exports are back: %s", strings.TrimSpace(lastLines(string(out), 2)))
	}
	return nil
}

func (h *helperServer) nfsExportSave(req helperRequest, logf func(string)) (any, error) {
	if req.Export == nil {
		return nil, uerr("The export is missing.")
	}
	e := *req.Export
	e.Comment = strings.TrimSpace(e.Comment)
	if !commentRe.MatchString(e.Comment) {
		return nil, uerr("The description may only use letters, digits and simple punctuation (60 at most).")
	}
	if err := validNFSPath(e.Path); err != nil {
		return nil, err
	}
	real, _, err := h.resolve(e.Path, true)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return nil, uerr("Choose a folder, not a file.")
	}
	if err := validNFSPath(real); err != nil {
		return nil, err
	}
	e.Path = real
	if e.Clients, err = validNFSClients(e.Clients); err != nil {
		return nil, err
	}
	sharesMu.Lock()
	defer sharesMu.Unlock()
	st := loadShares()
	idx := -1
	for i, o := range st.NFS {
		if o.ID == e.ID && e.ID != "" {
			idx = i
		} else if o.Path == e.Path {
			return nil, uerr("%s is already exported. Edit that export instead.", e.Path)
		}
	}
	if idx < 0 {
		e.ID = randomHex8()
		st.NFS = append(st.NFS, e)
	} else {
		st.NFS[idx] = e
	}
	if err := h.applyNFS(st); err != nil {
		return nil, err
	}
	return e, writeJSONFile(fwPath("shares.json"), st)
}

func (h *helperServer) nfsExportDelete(req helperRequest, logf func(string)) (any, error) {
	sharesMu.Lock()
	defer sharesMu.Unlock()
	st := loadShares()
	out := st.NFS[:0]
	found := false
	for _, e := range st.NFS {
		if e.ID == req.ID {
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		return nil, uerr("This export no longer exists.")
	}
	st.NFS = out
	if err := h.applyNFS(st); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, writeJSONFile(fwPath("shares.json"), st)
}

func randomHex8() string { return randomToken()[:8] }

// filesStat tells the web process whether a path is a file or folder inside the allowed roots
// (for share links). It is checked again when the link is used.
func (h *helperServer) filesStat(req helperRequest, logf func(string)) (any, error) {
	real, _, err := h.resolve(req.Path, true)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(real)
	if err != nil {
		return nil, uerr("This file or folder does not exist.")
	}
	if !fi.IsDir() && !fi.Mode().IsRegular() {
		return nil, uerr("Only files and folders can be shared.")
	}
	return map[string]any{"dir": fi.IsDir(), "size": fi.Size(), "name": filepath.Base(real)}, nil
}
