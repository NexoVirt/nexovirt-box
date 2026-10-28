package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func waitStatus(t *testing.T, js *jobs, id, want string) *job {
	t.Helper()
	for i := 0; i < 200; i++ {
		if j := js.get(id); j != nil {
			j.mu.Lock()
			st := j.Status
			j.mu.Unlock()
			if st == want {
				return j
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s never reached %s", id, want)
	return nil
}

func TestJobsSurviveRestartAsInterrupted(t *testing.T) {
	db := filepath.Join(t.TempDir(), "box.db")
	s1, err := openStore(db)
	if err != nil {
		t.Fatal(err)
	}
	js := newJobs(s1)
	block := make(chan struct{})
	j, err := js.start("start", "code-server", func(ctx context.Context, logf func(string)) (any, error) {
		logf("Pulling image")
		<-block
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.start("start", "code-server", nil); err == nil {
		t.Fatal("a second job for the same app was started")
	}
	time.Sleep(50 * time.Millisecond)
	s1.db.Close() // the web process dies here
	close(block)

	s2, _ := openStore(db)
	defer s2.db.Close()
	js2 := newJobs(s2)
	got := js2.get(j.ID)
	if got == nil || got.Status != "interrupted" || got.Error == "" || len(got.Lines) == 0 {
		t.Fatalf("after restart: %+v", got)
	}
	if len(js2.running()) != 0 {
		t.Fatal("an interrupted job still blocks the app")
	}
	if _, err := js2.start("start", "code-server", func(ctx context.Context, logf func(string)) (any, error) { return nil, nil }); err != nil {
		t.Fatalf("app still locked after restart: %v", err)
	}
}

func TestJobWatchdogStopsStalledJob(t *testing.T) {
	s, _ := openStore(filepath.Join(t.TempDir(), "box.db"))
	defer s.db.Close()
	js := newJobs(s)
	js.stall = 80 * time.Millisecond
	cancelled := make(chan struct{})
	j, _ := js.start("install", "code-server", func(ctx context.Context, logf func(string)) (any, error) {
		<-ctx.Done() // the helper call is cancelled
		close(cancelled)
		return nil, ctx.Err()
	})
	got := waitStatus(t, js, j.ID, "failed")
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("the stalled helper call was not cancelled")
	}
	time.Sleep(20 * time.Millisecond)
	got.mu.Lock()
	defer got.mu.Unlock()
	if got.Error == "" || got.Status != "failed" {
		t.Fatalf("stalled job: %+v", got)
	}
	if stored := s.loadJob(j.ID); stored == nil || stored.Status != "failed" {
		t.Fatalf("stalled job not saved: %+v", stored)
	}
}

func TestParsePSStates(t *testing.T) {
	cases := map[string]string{
		``: "not_created",
		`{"Service":"a","State":"running","ExitCode":0}`:                                                     "running",
		`{"Service":"a","State":"exited","ExitCode":0}`:                                                      "stopped",
		`{"Service":"a","State":"exited","ExitCode":137}`:                                                    "exited",
		"{\"Service\":\"a\",\"State\":\"running\"}\n{\"Service\":\"b\",\"State\":\"exited\",\"ExitCode\":1}": "partial",
		`[{"Service":"a","State":"restarting","ExitCode":1}]`:                                                "restarting",
	}
	for in, want := range cases {
		if _, got, _ := parsePS([]byte(in)); got != want {
			t.Errorf("%q: got %s want %s", in, got, want)
		}
	}
	if _, _, code := parsePS([]byte(`{"Service":"a","State":"exited","ExitCode":137}`)); code != 137 {
		t.Errorf("exit code %d", code)
	}
}

func TestClaimRefusesSecondActionAndReportsBusy(t *testing.T) {
	h := &helperServer{}
	release, err := h.claim("code-server", "install")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.claim("code-server", "start"); err == nil {
		t.Fatal("a second action was allowed while the install runs")
	}
	if h.busyOp("code-server") != "install" {
		t.Fatal("the running action is not reported")
	}
	release()
	if h.busyOp("code-server") != "" {
		t.Fatal("still busy after release")
	}
	r2, err := h.claim("code-server", "start")
	if err != nil {
		t.Fatal(err)
	}
	r2()
}
