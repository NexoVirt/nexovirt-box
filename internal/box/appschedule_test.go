package box

import (
	"strings"
	"testing"
	"time"
)

func TestAppScheduleDue(t *testing.T) {
	var s appSchedule
	s.On = true
	s.Days[time.Monday] = daySchedule{Start: "07:00", Stop: "19:30"}
	mon := func(h, m int) time.Time { return time.Date(2026, 11, 2, h, m, 0, 0, time.Local) } // a Monday
	cases := map[time.Time]string{mon(7, 0): "start", mon(7, 9): "start", mon(7, 10): "", mon(6, 59): "", mon(19, 31): "stop", mon(12, 0): ""}
	for at, want := range cases {
		if got := strings.Join(dueActions(s, at, 10*time.Minute), ","); got != want {
			t.Errorf("%s: %q, want %q", at.Format("15:04"), got, want)
		}
	}
	if got := dueActions(s, mon(7, 0).AddDate(0, 0, 1), 10*time.Minute); len(got) != 0 {
		t.Fatalf("Tuesday has no plan: %v", got)
	}
	s.On = false
	if len(dueActions(s, mon(7, 0), 10*time.Minute)) != 0 {
		t.Fatal("a switched-off schedule ran")
	}
	bad := appSchedule{}
	bad.Days[0] = daySchedule{Start: "7:00"}
	if bad.validate() == nil {
		t.Fatal("bad time accepted")
	}
	bad.Days[0] = daySchedule{Start: "07:00", Stop: "07:00"}
	if bad.validate() == nil {
		t.Fatal("same start and stop accepted")
	}
}
