package box

// External drives: USB disks the server sees right now (lsblk TRAN=usb or removable), their
// partitions and file systems. The helper mounts a partition under /media/nxbox/<name> with
// nosuid,nodev,noexec (only file systems from a fixed list), unmounts and powers a disk off for a
// safe eject, and formats only after the admin typed "FORMAT <device>" (one ext4 or exFAT
// partition). Every device must be in the current lsblk list and on a USB/removable disk, so the
// server's own disks can never be touched. Mounts are not made permanent (a reboot unmounts).

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	mediaBase     = "/media/nxbox"
	driveFSAllow  = map[string]bool{"ext4": true, "ext3": true, "ext2": true, "xfs": true, "btrfs": true, "vfat": true, "exfat": true, "ntfs": true, "ntfs3": true}
	mountNameRe   = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	driveLabelRe  = regexp.MustCompile(`^[A-Za-z0-9 ._-]{0,16}$`)
	partPathRe    = regexp.MustCompile(`^/dev/[a-z0-9]+$`)
	formatFSAllow = map[string]bool{"ext4": true, "exfat": true}
)

type drivePart struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	FSType     string `json:"fstype"`
	Label      string `json:"label"`
	UUID       string `json:"uuid"`
	Mountpoint string `json:"mountpoint"`
	Ours       bool   `json:"ours"` // mounted by Box under /media/nxbox
}

type externalDrive struct {
	Path   string      `json:"path"`
	Model  string      `json:"model"`
	Vendor string      `json:"vendor"`
	Size   int64       `json:"size"`
	Parts  []drivePart `json:"parts"`
}

// parseDrives reads `lsblk -J -b -o NAME,PATH,TYPE,SIZE,MODEL,VENDOR,TRAN,RM,FSTYPE,LABEL,UUID,MOUNTPOINT`.
func parseDrives(out []byte) ([]externalDrive, error) {
	type dev struct {
		Path       flex  `json:"path"`
		Type       flex  `json:"type"`
		Size       flex  `json:"size"`
		Model      flex  `json:"model"`
		Vendor     flex  `json:"vendor"`
		Tran       flex  `json:"tran"`
		RM         flex  `json:"rm"`
		FSType     flex  `json:"fstype"`
		Label      flex  `json:"label"`
		UUID       flex  `json:"uuid"`
		Mountpoint flex  `json:"mountpoint"`
		Children   []dev `json:"children"`
	}
	var res struct {
		Blockdevices []dev `json:"blockdevices"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, uerr("The drives could not be listed.")
	}
	num := func(f flex) int64 { var n int64; json.Unmarshal([]byte(f), &n); return n }
	part := func(d dev) drivePart {
		mp := string(d.Mountpoint)
		return drivePart{Path: string(d.Path), Size: num(d.Size), FSType: string(d.FSType), Label: string(d.Label), UUID: string(d.UUID),
			Mountpoint: mp, Ours: strings.HasPrefix(mp, mediaBase+"/")}
	}
	out2 := []externalDrive{}
	for _, d := range res.Blockdevices {
		usb := string(d.Tran) == "usb" || string(d.RM) == "1" || string(d.RM) == "true"
		if string(d.Type) != "disk" || !usb || !partPathRe.MatchString(string(d.Path)) {
			continue
		}
		x := externalDrive{Path: string(d.Path), Model: string(d.Model), Vendor: string(d.Vendor), Size: num(d.Size), Parts: []drivePart{}}
		for _, c := range d.Children {
			if partPathRe.MatchString(string(c.Path)) {
				x.Parts = append(x.Parts, part(c))
			}
		}
		if len(d.Children) == 0 && d.FSType != "" { // a file system right on the disk
			x.Parts = append(x.Parts, part(d))
		}
		out2 = append(out2, x)
	}
	return out2, nil
}

func listDrives() ([]externalDrive, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsblk", "-J", "-b", "-o", "NAME,PATH,TYPE,SIZE,MODEL,VENDOR,TRAN,RM,FSTYPE,LABEL,UUID,MOUNTPOINT").Output()
	if err != nil {
		return nil, uerr("The drives could not be listed (lsblk failed).")
	}
	return parseDrives(out)
}

// findPart returns the partition (and its disk) for a path, only on an external drive.
func findPart(drives []externalDrive, path string) (externalDrive, drivePart, bool) {
	for _, d := range drives {
		for _, p := range d.Parts {
			if p.Path == path {
				return d, p, true
			}
		}
	}
	return externalDrive{}, drivePart{}, false
}

func mountName(p drivePart) string {
	n := mountNameRe.ReplaceAllString(p.Label, "-")
	n = strings.Trim(n, "-.")
	if n == "" {
		n = strings.TrimPrefix(p.Path, "/dev/")
	}
	if len(n) > 40 {
		n = n[:40]
	}
	return n
}

func (h *helperServer) drivesList(req helperRequest, _ func(string)) (any, error) {
	ds, err := listDrives()
	if err != nil {
		return nil, err
	}
	return map[string]any{"drives": ds}, nil
}

func (h *helperServer) driveMount(req helperRequest, _ func(string)) (any, error) {
	ds, err := listDrives()
	if err != nil {
		return nil, err
	}
	_, p, ok := findPart(ds, req.Path)
	if !ok {
		return nil, uerr("This drive was not found. Plug it in again.")
	}
	if p.Mountpoint != "" {
		return map[string]string{"mountpoint": p.Mountpoint}, nil
	}
	if !driveFSAllow[p.FSType] {
		return nil, uerr("Box cannot mount this drive (file system %q). Format it first, or use a drive with ext4, exFAT, FAT or NTFS.", p.FSType)
	}
	dir := filepath.Join(mediaBase, mountName(p))
	for n := 2; isMountpoint(dir) || nonEmpty(dir); n++ {
		dir = filepath.Join(mediaBase, mountName(p)+"-"+strconv.Itoa(n))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	opts := "nosuid,nodev,noexec"
	if p.FSType == "vfat" || p.FSType == "exfat" || p.FSType == "ntfs" || p.FSType == "ntfs3" {
		opts += ",uid=0,gid=0,umask=022"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "mount", "-o", opts, "--", p.Path, dir).CombinedOutput(); err != nil {
		os.Remove(dir)
		return nil, uerr("The drive could not be mounted: %s", strings.TrimSpace(lastLine(string(out))))
	}
	return map[string]string{"mountpoint": dir}, nil
}

func isMountpoint(dir string) bool {
	b, _ := os.ReadFile("/proc/self/mountinfo")
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) > 4 && f[4] == dir {
			return true
		}
	}
	return false
}

func nonEmpty(dir string) bool {
	es, err := os.ReadDir(dir)
	return err == nil && len(es) > 0
}

// driveUnmount unmounts every partition of the drive Box mounted (Op "eject" also powers it off).
func (h *helperServer) driveUnmount(req helperRequest, _ func(string)) (any, error) {
	ds, err := listDrives()
	if err != nil {
		return nil, err
	}
	var disk externalDrive
	found := false
	for _, d := range ds {
		if d.Path == req.Path {
			disk, found = d, true
		}
	}
	if !found {
		return nil, uerr("This drive was not found.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	exec.CommandContext(ctx, "sync").Run()
	for _, p := range disk.Parts {
		if p.Mountpoint == "" {
			continue
		}
		if !p.Ours {
			return nil, uerr("%s is mounted at %s by something else than Box. Unmount it there first.", p.Path, p.Mountpoint)
		}
		if out, err := exec.CommandContext(ctx, "umount", "--", p.Mountpoint).CombinedOutput(); err != nil {
			return nil, uerr("%s is still in use and was not unmounted: %s", p.Mountpoint, strings.TrimSpace(lastLine(string(out))))
		}
		os.Remove(p.Mountpoint) // the empty mount folder Box made
	}
	if req.Op == "eject" {
		dev := strings.TrimPrefix(disk.Path, "/dev/")
		if _, err := exec.LookPath("udisksctl"); err == nil {
			exec.CommandContext(ctx, "udisksctl", "power-off", "-b", disk.Path).Run()
		} else {
			os.WriteFile("/sys/block/"+dev+"/device/delete", []byte("1"), 0o200)
		}
	}
	return map[string]bool{"ok": true}, nil
}

// driveFormat wipes an external drive and makes one partition (ext4 or exFAT). It needs the
// exact text "FORMAT <device>" and refuses while anything on the drive is mounted.
func (h *helperServer) driveFormat(ctx context.Context, req helperRequest, logf func(string)) (any, error) {
	if !h.consoleAllowed(req) {
		return nil, uerr("Enter your password to format a drive.")
	}
	if req.Confirm != "FORMAT "+req.Path {
		return nil, uerr("Type FORMAT %s to confirm. Everything on the drive is deleted.", req.Path)
	}
	if !formatFSAllow[req.Op] {
		return nil, uerr("Pick ext4 or exFAT.")
	}
	if !driveLabelRe.MatchString(req.Name) {
		return nil, uerr("The name can have up to 16 letters, digits, spaces, dots and dashes.")
	}
	ds, err := listDrives()
	if err != nil {
		return nil, err
	}
	var disk externalDrive
	found := false
	for _, d := range ds {
		if d.Path == req.Path {
			disk, found = d, true
		}
	}
	if !found {
		return nil, uerr("This drive was not found. Only external (USB) drives can be formatted here.")
	}
	for _, p := range disk.Parts {
		if p.Mountpoint != "" {
			return nil, uerr("Unmount the drive first (%s is mounted at %s).", p.Path, p.Mountpoint)
		}
	}
	mkfs := map[string]string{"ext4": "mkfs.ext4", "exfat": "mkfs.exfat"}[req.Op]
	if _, err := exec.LookPath(mkfs); err != nil {
		return nil, uerr("%s is not installed on this server.", mkfs)
	}
	if _, err := exec.LookPath("sfdisk"); err != nil {
		return nil, uerr("sfdisk is not installed on this server.")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	logf("Writing a new partition table on " + disk.Path)
	part := exec.CommandContext(ctx, "sfdisk", "--wipe", "always", "--label", "gpt", "--", disk.Path)
	part.Stdin = strings.NewReader("type=linux\n")
	if req.Op == "exfat" {
		part.Stdin = strings.NewReader("type=EBD0A0A2-B9E5-4433-87C0-68B6B72699C7\n")
	}
	if out, err := part.CombinedOutput(); err != nil {
		return nil, uerr("The partition table could not be written: %s", strings.TrimSpace(lastLine(string(out))))
	}
	exec.CommandContext(ctx, "udevadm", "settle").Run()
	ds, _ = listDrives()
	var newPart string
	for _, d := range ds {
		if d.Path == disk.Path && len(d.Parts) == 1 {
			newPart = d.Parts[0].Path
		}
	}
	if newPart == "" {
		return nil, uerr("The new partition did not show up. Unplug the drive and plug it in again.")
	}
	logf("Creating the file system on " + newPart)
	args := []string{"-L", req.Name, "--", newPart}
	if req.Op == "ext4" {
		args = append([]string{"-F"}, args...)
	} else {
		args = []string{"-n", req.Name, "--", newPart}
	}
	if err := runStream(ctx, exec.CommandContext(ctx, mkfs, args...), logf); err != nil {
		return nil, uerr("The file system could not be created.")
	}
	return map[string]string{"partition": newPart}, nil
}

// ---- web ----

func (a *App) drivesGet(w http.ResponseWriter, r *http.Request) {
	a.helperJSON(w, r, helperRequest{Action: "drives_list"})
}

type driveBody struct {
	Path    string `json:"path"`
	FS      string `json:"fs"`
	Name    string `json:"name"`
	Confirm string `json:"confirm"`
}

func (a *App) driveAction(action, op, audit string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in driveBody
		if err := readJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !partPathRe.MatchString(in.Path) {
			writeError(w, http.StatusUnprocessableEntity, "This drive is not valid.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		raw, err := a.helper.call(ctx, helperRequest{Action: action, Path: in.Path, Op: op}, nil)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, friendly(err))
			return
		}
		a.audit(r, audit, in.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}
}

func (a *App) driveFormatStart(w http.ResponseWriter, r *http.Request) {
	var in driveBody
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !partPathRe.MatchString(in.Path) {
		writeError(w, http.StatusUnprocessableEntity, "This drive is not valid.")
		return
	}
	if !a.needProof(w, r) {
		return
	}
	a.runJob(w, r, "format", "drive-"+strings.TrimPrefix(in.Path, "/dev/"), "drive.format",
		a.proofReq(r, helperRequest{Action: "drive_format", Path: in.Path, Op: in.FS, Name: in.Name, Confirm: in.Confirm}))
}
