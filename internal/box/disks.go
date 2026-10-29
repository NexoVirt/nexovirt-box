package box

// Disk health, root side: physical disks from lsblk, SMART through smartctl --json (installed on
// request), a health verdict with the attributes that predict failure, and self-tests. Only
// devices lsblk lists right now can be tested; the path is never taken from the browser as is.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type diskInfo struct {
	Name    string        `json:"name"`
	Path    string        `json:"path"`
	Size    int64         `json:"size"`
	Model   string        `json:"model"`
	Serial  string        `json:"serial"`
	Kind    string        `json:"kind"` // hdd | ssd | nvme
	Tran    string        `json:"tran"`
	Virtual bool          `json:"virtual"`
	Smart   *smartSummary `json:"smart,omitempty"`
}

// flex reads lsblk values that are numbers, bools or strings depending on the util-linux version.
type flex string

func (f *flex) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch x := v.(type) {
	case nil:
		*f = ""
	case string:
		*f = flex(strings.TrimSpace(x))
	case bool:
		*f = flex(strconv.FormatBool(x))
	case float64:
		*f = flex(strconv.FormatFloat(x, 'f', 0, 64))
	}
	return nil
}

var virtualRe = regexp.MustCompile(`(?i)qemu|vbox|virtualbox|vmware|virtual|virtio|xen|hyper-v|msft`)

func parseLsblk(b []byte) ([]diskInfo, error) {
	var out struct {
		Devices []struct {
			Name, Path, Type, Size, Model, Serial, Rota, Tran, Vendor flex
		} `json:"blockdevices"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	disks := []diskInfo{}
	for _, d := range out.Devices {
		name := string(d.Name)
		if d.Type != "disk" || strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "loop") {
			continue
		}
		size, _ := strconv.ParseInt(string(d.Size), 10, 64)
		di := diskInfo{Name: name, Path: string(d.Path), Size: size, Model: string(d.Model), Serial: string(d.Serial), Tran: string(d.Tran)}
		if di.Path == "" {
			di.Path = "/dev/" + name
		}
		switch {
		case strings.HasPrefix(name, "nvme") || d.Tran == "nvme":
			di.Kind = "nvme"
		case d.Rota == "true" || d.Rota == "1":
			di.Kind = "hdd"
		default:
			di.Kind = "ssd"
		}
		di.Virtual = virtualRe.MatchString(string(d.Model)+" "+string(d.Vendor)) ||
			strings.HasPrefix(name, "vd") || strings.HasPrefix(name, "xvd")
		disks = append(disks, di)
	}
	return disks, nil
}

type selfTest struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Passed bool   `json:"passed"`
	Hours  int64  `json:"hours"`
}

type smartSummary struct {
	Available     bool       `json:"available"`
	Reason        string     `json:"reason,omitempty"` // virtual | unsupported | not_installed | standby | error
	Message       string     `json:"message,omitempty"`
	Passed        *bool      `json:"passed,omitempty"`
	Verdict       string     `json:"verdict"` // good | warning | failing | unknown
	Warnings      []string   `json:"warnings"`
	Temperature   *int64     `json:"temperature,omitempty"`
	PowerOnHours  *int64     `json:"power_on_hours,omitempty"`
	Reallocated   *int64     `json:"reallocated,omitempty"`
	Pending       *int64     `json:"pending,omitempty"`
	Uncorrectable *int64     `json:"uncorrectable,omitempty"`
	MediaErrors   *int64     `json:"media_errors,omitempty"`
	WearUsed      *int64     `json:"wear_used,omitempty"` // percent of the rated life used (SSD/NVMe)
	Testing       bool       `json:"testing"`
	Remaining     int64      `json:"remaining,omitempty"` // percent of the running test left
	Tests         []selfTest `json:"tests"`
}

func p64(v int64) *int64 { return &v }

// parseSmart turns smartctl --json -a output into the summary the UI shows.
func parseSmart(b []byte) smartSummary {
	s := smartSummary{Verdict: "unknown", Warnings: []string{}, Tests: []selfTest{}}
	var j struct {
		Smartctl struct {
			Messages []struct{ String, Severity string } `json:"messages"`
		} `json:"smartctl"`
		Support struct {
			Available bool `json:"available"`
			Enabled   bool `json:"enabled"`
		} `json:"smart_support"`
		Status *struct {
			Passed bool `json:"passed"`
		} `json:"smart_status"`
		Temperature *struct {
			Current int64 `json:"current"`
		} `json:"temperature"`
		PowerOn *struct {
			Hours int64 `json:"hours"`
		} `json:"power_on_time"`
		Attrs struct {
			Table []struct {
				ID    int   `json:"id"`
				Value int64 `json:"value"`
				Raw   struct {
					Value int64 `json:"value"`
				} `json:"raw"`
			} `json:"table"`
		} `json:"ata_smart_attributes"`
		AtaData struct {
			SelfTest struct {
				Status struct {
					Value     int64 `json:"value"`
					Remaining int64 `json:"remaining_percent"`
				} `json:"status"`
			} `json:"self_test"`
		} `json:"ata_smart_data"`
		AtaLog struct {
			Standard struct {
				Table []struct {
					Type struct {
						String string `json:"string"`
					} `json:"type"`
					Status struct {
						String string `json:"string"`
						Passed bool   `json:"passed"`
					} `json:"status"`
					Hours int64 `json:"lifetime_hours"`
				} `json:"table"`
			} `json:"standard"`
		} `json:"ata_smart_self_test_log"`
		NVMe *struct {
			Critical  int64 `json:"critical_warning"`
			Temp      int64 `json:"temperature"`
			Spare     int64 `json:"available_spare"`
			SpareMin  int64 `json:"available_spare_threshold"`
			Used      int64 `json:"percentage_used"`
			Hours     int64 `json:"power_on_hours"`
			MediaErrs int64 `json:"media_errors"`
		} `json:"nvme_smart_health_information_log"`
		NVMeLog struct {
			Current struct {
				Value int64 `json:"value"`
			} `json:"current_self_test_operation"`
			Completion int64 `json:"current_self_test_completion_percent"`
			Table      []struct {
				Code struct {
					String string `json:"string"`
				} `json:"self_test_code"`
				Result struct {
					Value  int64  `json:"value"`
					String string `json:"string"`
				} `json:"self_test_result"`
				Hours int64 `json:"power_on_hours"`
			} `json:"table"`
		} `json:"nvme_self_test_log"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		s.Reason, s.Message = "error", "smartctl gave no readable answer."
		return s
	}
	if !j.Support.Available && j.NVMe == nil {
		s.Reason = "unsupported"
		for _, m := range j.Smartctl.Messages {
			if m.Severity == "error" {
				s.Message = m.String
			}
		}
		return s
	}
	s.Available = true
	if j.Temperature != nil {
		s.Temperature = p64(j.Temperature.Current)
	}
	if j.PowerOn != nil {
		s.PowerOnHours = p64(j.PowerOn.Hours)
	}
	for _, a := range j.Attrs.Table {
		switch a.ID {
		case 5:
			s.Reallocated = p64(a.Raw.Value)
		case 197:
			s.Pending = p64(a.Raw.Value)
		case 198:
			s.Uncorrectable = p64(a.Raw.Value)
		case 177, 231, 233, 202: // wear levelling / life left, normalized 100 = new
			if s.WearUsed == nil && a.Value > 0 && a.Value <= 100 {
				s.WearUsed = p64(100 - a.Value)
			}
		}
	}
	if v := j.AtaData.SelfTest.Status.Value; v>>4 == 0xf {
		s.Testing, s.Remaining = true, j.AtaData.SelfTest.Status.Remaining
		if s.Remaining == 0 {
			s.Remaining = (v & 0xf) * 10
		}
	}
	for _, t := range j.AtaLog.Standard.Table {
		s.Tests = append(s.Tests, selfTest{Type: t.Type.String, Status: t.Status.String, Passed: t.Status.Passed, Hours: t.Hours})
	}
	if n := j.NVMe; n != nil {
		s.Temperature, s.PowerOnHours, s.WearUsed, s.MediaErrors = p64(n.Temp), p64(n.Hours), p64(n.Used), p64(n.MediaErrs)
		if n.Critical != 0 {
			s.Warnings = append(s.Warnings, "The drive reports a critical warning.")
		}
		if n.SpareMin > 0 && n.Spare < n.SpareMin {
			s.Warnings = append(s.Warnings, "The spare area is below its threshold.")
		}
		if n.MediaErrs > 0 {
			s.Warnings = append(s.Warnings, fmt.Sprintf("%d media errors.", n.MediaErrs))
		}
		if j.NVMeLog.Current.Value != 0 {
			s.Testing, s.Remaining = true, 100-j.NVMeLog.Completion
		}
		for _, t := range j.NVMeLog.Table {
			s.Tests = append(s.Tests, selfTest{Type: t.Code.String, Status: t.Result.String, Passed: t.Result.Value == 0, Hours: t.Hours})
		}
	}
	if len(s.Tests) > 10 {
		s.Tests = s.Tests[:10]
	}
	if v := s.Reallocated; v != nil && *v > 0 {
		s.Warnings = append(s.Warnings, fmt.Sprintf("%d reallocated sectors.", *v))
	}
	if v := s.Pending; v != nil && *v > 0 {
		s.Warnings = append(s.Warnings, fmt.Sprintf("%d sectors waiting to be reallocated.", *v))
	}
	if v := s.Uncorrectable; v != nil && *v > 0 {
		s.Warnings = append(s.Warnings, fmt.Sprintf("%d sectors that cannot be read.", *v))
	}
	if v := s.WearUsed; v != nil && *v >= 90 {
		s.Warnings = append(s.Warnings, fmt.Sprintf("%d %% of its rated life is used.", *v))
	}
	if v := s.Temperature; v != nil && *v >= 60 {
		s.Warnings = append(s.Warnings, fmt.Sprintf("It is hot (%d °C).", *v))
	}
	if len(s.Tests) > 0 && !s.Tests[0].Passed && !strings.Contains(strings.ToLower(s.Tests[0].Status), "abort") &&
		!strings.Contains(strings.ToLower(s.Tests[0].Status), "progress") {
		s.Warnings = append(s.Warnings, "The last self-test did not pass.")
	}
	switch {
	case j.Status != nil && !j.Status.Passed:
		s.Verdict = "failing"
		f := false
		s.Passed = &f
	case len(s.Warnings) > 0:
		s.Verdict = "warning"
	default:
		s.Verdict = "good"
	}
	if j.Status != nil && j.Status.Passed {
		t := true
		s.Passed = &t
	}
	return s
}

func listDisks() ([]diskInfo, error) {
	out, err := exec.Command("lsblk", "-J", "-b", "-d", "-o", "NAME,PATH,TYPE,SIZE,MODEL,SERIAL,ROTA,TRAN,VENDOR").Output()
	if err != nil {
		return nil, uerr("The disks could not be listed (lsblk failed).")
	}
	return parseLsblk(out)
}

func smartFor(d diskInfo) *smartSummary {
	if _, err := exec.LookPath("smartctl"); err != nil {
		return &smartSummary{Reason: "not_installed", Verdict: "unknown", Warnings: []string{}, Tests: []selfTest{}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// -n standby: do not spin up a sleeping disk just to read it. The exit code is a bit mask, the JSON counts.
	out, _ := exec.CommandContext(ctx, "smartctl", "--json=c", "-a", "-n", "standby", d.Path).Output()
	s := parseSmart(out)
	if strings.Contains(string(out), "STANDBY") {
		s.Reason, s.Message = "standby", "The disk is asleep, so Box did not wake it up."
	}
	if !s.Available && d.Virtual {
		s.Reason = "virtual"
	}
	return &s
}

func (h *helperServer) disksList(req helperRequest, logf func(string)) (any, error) {
	disks, err := listDisks()
	if err != nil {
		return nil, err
	}
	for i := range disks {
		disks[i].Smart = smartFor(disks[i])
	}
	_, smart := exec.LookPath("smartctl")
	return map[string]any{"disks": disks, "smartctl": smart == nil}, nil
}

var devPathRe = regexp.MustCompile(`^/dev/[a-z0-9]+$`)

func validSelfTest(disks []diskInfo, path, kind string) error {
	if kind != "short" && kind != "long" {
		return uerr("Choose a short or a long test.")
	}
	if !devPathRe.MatchString(path) {
		return uerr("This disk is not valid.")
	}
	for _, d := range disks {
		if d.Path == path {
			return nil
		}
	}
	return uerr("This disk was not found.")
}

func (h *helperServer) smartSelftest(req helperRequest, logf func(string)) (any, error) {
	disks, err := listDisks()
	if err != nil {
		return nil, err
	}
	if err := validSelfTest(disks, req.Path, req.Op); err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("smartctl"); err != nil {
		return nil, uerr("Install smartmontools first.")
	}
	out, err := exec.Command("smartctl", "-t", req.Op, req.Path).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "Testing has begun") && !strings.Contains(string(out), "Self-test has begun") {
		return nil, uerr("The disk did not start the test: %s", strings.TrimSpace(lastLines(string(out), 1)))
	}
	return map[string]bool{"ok": true}, nil
}
