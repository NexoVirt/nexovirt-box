package main

import "testing"

func TestParseLsblk(t *testing.T) {
	out := `{"blockdevices":[
	 {"name":"sda","path":"/dev/sda","type":"disk","size":17179869184,"model":"QEMU HARDDISK","serial":"drive-scsi0","rota":true,"tran":null,"vendor":"QEMU    "},
	 {"name":"nvme0n1","path":"/dev/nvme0n1","type":"disk","size":"512110190592","model":"Samsung SSD 980","serial":"S64","rota":"0","tran":"nvme","vendor":null},
	 {"name":"sr0","path":"/dev/sr0","type":"rom","size":4194304,"model":"QEMU DVD-ROM"},
	 {"name":"zram0","path":"/dev/zram0","type":"disk","size":1000},
	 {"name":"vda","path":"/dev/vda","type":"disk","size":1000,"model":null}
	]}`
	disks, err := parseLsblk([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 3 {
		t.Fatalf("got %d disks: %+v", len(disks), disks)
	}
	if !disks[0].Virtual || disks[0].Size != 17179869184 || disks[0].Kind != "hdd" {
		t.Fatalf("sda: %+v", disks[0])
	}
	if disks[1].Virtual || disks[1].Kind != "nvme" || disks[1].Size != 512110190592 {
		t.Fatalf("nvme: %+v", disks[1])
	}
	if !disks[2].Virtual {
		t.Fatalf("vda should count as virtual: %+v", disks[2])
	}
}

func TestParseSmartHealthyHDD(t *testing.T) {
	out := `{"smart_support":{"available":true,"enabled":true},"smart_status":{"passed":true},
	 "temperature":{"current":34},"power_on_time":{"hours":12000},
	 "ata_smart_attributes":{"table":[
	  {"id":5,"name":"Reallocated_Sector_Ct","value":100,"thresh":10,"raw":{"value":0}},
	  {"id":197,"name":"Current_Pending_Sector","value":100,"thresh":0,"raw":{"value":0}},
	  {"id":198,"name":"Offline_Uncorrectable","value":100,"thresh":0,"raw":{"value":0}}]},
	 "ata_smart_data":{"self_test":{"status":{"value":0,"string":"completed without error"}}},
	 "ata_smart_self_test_log":{"standard":{"table":[{"type":{"string":"Short offline"},"status":{"string":"Completed without error","passed":true},"lifetime_hours":11990}]}}}`
	s := parseSmart([]byte(out))
	if s.Verdict != "good" || !s.Available || *s.Temperature != 34 || *s.PowerOnHours != 12000 || *s.Reallocated != 0 {
		t.Fatalf("%+v", s)
	}
	if len(s.Tests) != 1 || !s.Tests[0].Passed || s.Testing {
		t.Fatalf("tests %+v", s)
	}
}

func TestParseSmartWarningAndFailing(t *testing.T) {
	warn := `{"smart_support":{"available":true,"enabled":true},"smart_status":{"passed":true},
	 "ata_smart_attributes":{"table":[{"id":5,"raw":{"value":8}},{"id":197,"raw":{"value":2}}]},
	 "ata_smart_data":{"self_test":{"status":{"value":249,"remaining_percent":90}}}}`
	s := parseSmart([]byte(warn))
	if s.Verdict != "warning" || len(s.Warnings) != 2 || !s.Testing || s.Remaining != 90 {
		t.Fatalf("warning: %+v", s)
	}
	fail := `{"smart_support":{"available":true,"enabled":true},"smart_status":{"passed":false}}`
	if s := parseSmart([]byte(fail)); s.Verdict != "failing" {
		t.Fatalf("failing: %+v", s)
	}
}

func TestParseSmartNVMe(t *testing.T) {
	out := `{"smart_support":{"available":true,"enabled":true},"smart_status":{"passed":true},
	 "nvme_smart_health_information_log":{"critical_warning":0,"temperature":41,"available_spare":100,"available_spare_threshold":10,"percentage_used":93,"power_on_hours":800,"media_errors":0},
	 "nvme_self_test_log":{"current_self_test_operation":{"value":0},"table":[{"self_test_code":{"string":"Short"},"self_test_result":{"value":0,"string":"Completed without error"},"power_on_hours":790}]}}`
	s := parseSmart([]byte(out))
	if s.Verdict != "warning" || *s.WearUsed != 93 || *s.Temperature != 41 || len(s.Tests) != 1 || !s.Tests[0].Passed {
		t.Fatalf("%+v", s)
	}
}

func TestParseSmartVirtual(t *testing.T) {
	out := `{"smart_support":{"available":false},"smartctl":{"messages":[{"string":"device lacks SMART capability","severity":"error"}]}}`
	s := parseSmart([]byte(out))
	if s.Available || s.Verdict != "unknown" {
		t.Fatalf("%+v", s)
	}
}

func TestValidSelfTest(t *testing.T) {
	disks := []diskInfo{{Path: "/dev/sda"}}
	if err := validSelfTest(disks, "/dev/sda", "short"); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"/dev/sdb", "short"}, {"/dev/sda", "conveyance; rm"}, {"/dev/sda/../sdb", "long"}, {"sda", "long"}} {
		if validSelfTest(disks, c[0], c[1]) == nil {
			t.Fatalf("%v accepted", c)
		}
	}
}
