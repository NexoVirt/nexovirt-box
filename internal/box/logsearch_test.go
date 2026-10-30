package box

import "testing"

func TestLogSearchParsing(t *testing.T) {
	for msg, want := range map[string]string{
		"ERROR: db down": "error", `{"level":"error","msg":"x"}`: "error", "level=warn msg=slow": "warn",
		"Warning: deprecated": "warn", "DEBUG cache hit": "debug", "listening on :8080": "info", "error-free": "error",
	} {
		if got := levelOf(msg); got != want {
			t.Errorf("levelOf(%q) = %s, want %s", msg, got, want)
		}
	}
	out := []byte(`{"__REALTIME_TIMESTAMP":"1793320000000000","_SYSTEMD_UNIT":"nexovirt-box-helper.service","MESSAGE":"helper app_action: boom failed"}
{"__REALTIME_TIMESTAMP":"1793320001000000","_SYSTEMD_UNIT":"nexovirt-box.service","MESSAGE":[104,105]}
not json`)
	lines := parseJournal(out)
	if len(lines) != 2 || lines[0].Service != "nexovirt-box-helper" || lines[0].Level != "error" || lines[1].Msg != "hi" || lines[1].App != "box" {
		t.Fatalf("%+v", lines)
	}
	got := filterLogs(append(lines, sourcedLine{App: "wiki", Service: "web", Msg: "GET / 200", Level: "info"}), "", "warn")
	if len(got) != 1 || got[0].Level != "error" {
		t.Fatalf("level filter: %+v", got)
	}
	if got := filterLogs(lines, "HI", ""); len(got) != 1 {
		t.Fatalf("text filter: %+v", got)
	}
	if got := filterLogs([]sourcedLine{{App: "wiki", Service: "web", Msg: "x"}}, "wiki/web", ""); len(got) != 1 {
		t.Fatal("source filter")
	}
}
