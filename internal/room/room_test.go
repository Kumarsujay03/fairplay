package room

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func ready(t *testing.T) *Room {
	r := New()
	r.Load(0, "m1", 100, "host")
	if _, ok := r.Ready(0); !ok {
		t.Fatal("ready should change state")
	}
	return r
}

func TestPlayIsScheduledAhead(t *testing.T) {
	r := ready(t)
	st, ok := r.Play(1000, 800, "host")
	if !ok || !st.Playing || st.Anchor != 1800 {
		t.Fatalf("got %+v", st)
	}
	if p := st.PositionAt(1500); p != 0 {
		t.Fatalf("before anchor position must hold, got %v", p)
	}
	if p := st.PositionAt(3800); !near(p, 2) {
		t.Fatalf("want 2s, got %v", p)
	}
}

func TestPauseFreezesCurrentPosition(t *testing.T) {
	r := ready(t)
	r.Play(0, 1000, "host")
	st, _ := r.Pause(11000, ReasonHost, "host")
	if st.Playing || !near(st.Pos, 10) {
		t.Fatalf("got %+v", st)
	}
}

func TestSeekWhilePlayingKeepsPlayingAfterLead(t *testing.T) {
	r := ready(t)
	r.Play(0, 0, "host")
	st, _ := r.SeekTo(5000, 700, 42, "host")
	if !st.Playing || st.Pos != 42 || st.Anchor != 5700 {
		t.Fatalf("got %+v", st)
	}
}

func TestSeekClampsToDurationAndEncoder(t *testing.T) {
	r := ready(t)
	st, _ := r.SeekTo(0, 0, 500, "host")
	if st.Pos != 100 {
		t.Fatalf("clamp to duration, got %v", st.Pos)
	}
	r.SetPlayable(100, 30)
	st, _ = r.SeekTo(0, 0, 80, "host")
	if st.Pos != 28 {
		t.Fatalf("clamp to encoder limit-2, got %v", st.Pos)
	}
	st, _ = r.SeekTo(0, 0, -5, "host")
	if st.Pos != 0 {
		t.Fatalf("clamp to zero, got %v", st.Pos)
	}
}

func TestEndOfMediaPauses(t *testing.T) {
	r := ready(t)
	r.Play(0, 0, "host")
	st, ok := r.Tick(101000, 700)
	if !ok || st.Playing || st.Reason != ReasonEnded || st.Pos != 100 {
		t.Fatalf("got %+v", st)
	}
	st, _ = r.Play(102000, 700, "host")
	if st.Pos != 0 {
		t.Fatalf("play after end restarts, got %v", st.Pos)
	}
}

func TestIntermissionCountdownResumesTogether(t *testing.T) {
	r := ready(t)
	r.Play(0, 0, "host")
	st, _ := r.Intermission(10000, 60, "host")
	if st.Playing || st.Reason != ReasonIntermission || st.InterUntil != 70000 || !near(st.Pos, 10) {
		t.Fatalf("got %+v", st)
	}
	st, _ = r.ExtendIntermission(20000, 300)
	if st.InterUntil != 370000 {
		t.Fatalf("extend, got %v", st.InterUntil)
	}
	if _, ok := r.Tick(100000, 700); ok {
		t.Fatal("should still be in intermission")
	}
	st, ok := r.Tick(370000, 700)
	if !ok || !st.Playing || st.Anchor != 370700 || !near(st.Pos, 10) {
		t.Fatalf("got %+v", st)
	}
}

func TestEncoderGuardPausesAndResumes(t *testing.T) {
	r := ready(t)
	r.SetPlayable(100, 40)
	r.Play(0, 0, "host")
	st, ok := r.Tick(35000, 700)
	if !ok || st.Reason != ReasonEncoder || st.Playing {
		t.Fatalf("expected encoder pause, got %+v", st)
	}
	r.SetPlayable(100, 50)
	if _, ok := r.Tick(36000, 700); ok {
		t.Fatal("not enough headroom yet")
	}
	r.SetPlayable(100, -1)
	st, ok = r.Tick(37000, 700)
	if !ok || !st.Playing {
		t.Fatalf("expected resume, got %+v", st)
	}
}

func TestPlayRefusedWhileLoading(t *testing.T) {
	r := New()
	r.Load(0, "m1", 100, "host")
	if _, ok := r.Play(0, 0, "host"); ok {
		t.Fatal("must not play while loading")
	}
}

func TestVersionAlwaysIncreases(t *testing.T) {
	r := ready(t)
	v := r.State().Version
	r.Play(0, 0, "h")
	r.SeekTo(1, 0, 3, "h")
	r.Pause(2, ReasonHost, "h")
	if r.State().Version <= v+2 {
		t.Fatalf("version did not advance: %d -> %d", v, r.State().Version)
	}
	r.Load(3, "m2", 10, "h")
	if r.State().Version <= v+3 {
		t.Fatal("load must not reset version")
	}
}

func TestLeadBounds(t *testing.T) {
	cases := map[int64]int64{0: 700, 100: 700, 300: 1000, 1000: 2400, 5000: 2800}
	for rtt, want := range cases {
		if got := Lead(rtt); got != want {
			t.Errorf("Lead(%d)=%d want %d", rtt, got, want)
		}
	}
}
