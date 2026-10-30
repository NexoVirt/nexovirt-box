package box

import (
	"strings"
	"testing"
)

const lsblkDrives = `{"blockdevices":[
 {"path":"/dev/sda","type":"disk","size":512110190592,"model":"Samsung SSD","tran":"sata","rm":false,"children":[
   {"path":"/dev/sda1","type":"part","size":512,"fstype":"ext4","mountpoint":"/"}]},
 {"path":"/dev/sdb","type":"disk","size":64023257088,"model":"Cruzer","vendor":"SanDisk","tran":"usb","rm":true,"children":[
   {"path":"/dev/sdb1","type":"part","size":64000000000,"fstype":"exfat","label":"My Stick!","uuid":"AB-CD","mountpoint":null}]},
 {"path":"/dev/sdc","type":"disk","size":1000,"tran":"usb","rm":"1","fstype":"vfat","label":"","mountpoint":"/media/nxbox/sdc"}
]}`

func TestParseDrives(t *testing.T) {
	ds, err := parseDrives([]byte(lsblkDrives))
	if err != nil || len(ds) != 2 || ds[0].Path != "/dev/sdb" || ds[1].Path != "/dev/sdc" {
		t.Fatalf("%+v %v", ds, err)
	}
	if _, _, ok := findPart(ds, "/dev/sda1"); ok {
		t.Fatal("the server's own disk counts as external")
	}
	_, p, ok := findPart(ds, "/dev/sdb1")
	if !ok || p.FSType != "exfat" || mountName(p) != "My-Stick" {
		t.Fatalf("%+v %q", p, mountName(p))
	}
	_, p, _ = findPart(ds, "/dev/sdc")
	if !p.Ours || mountName(p) != "sdc" {
		t.Fatalf("whole-disk file system: %+v", p)
	}
}

func TestDriveFormatNeedsProofAndTypedConfirm(t *testing.T) {
	h := &helperServer{appsDir: t.TempDir(), webState: t.TempDir(), proofs: newProofKeeper()}
	_, err := h.driveFormat(t.Context(), helperRequest{Path: "/dev/sdb", Op: "ext4", Confirm: "FORMAT /dev/sdb"}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("formatted without a proof: %v", err)
	}
}
