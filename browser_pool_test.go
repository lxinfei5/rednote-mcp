package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-rod/rod"
)

func TestIsConnDeadErr(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("write tcp: use of closed network connection"), true},
		{errors.New("EOF"), true},
		{errors.New("Session with given id not found"), true},
		{errors.New("element not found"), false},
	}
	for _, c := range cases {
		if got := isConnDeadErr(c.err); got != c.want {
			t.Fatalf("isConnDeadErr(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestTimeoutFor(t *testing.T) {
	if timeoutFor(tabRead) != 30*time.Second {
		t.Fatalf("tabRead timeout = %v", timeoutFor(tabRead))
	}
	if timeoutFor(tabReadLong) != 3*time.Minute {
		t.Fatalf("tabReadLong timeout = %v", timeoutFor(tabReadLong))
	}
	if timeoutFor(tabWrite) != 5*time.Minute {
		t.Fatalf("tabWrite timeout = %v", timeoutFor(tabWrite))
	}
	if timeoutFor(tabWriteVideo) != 10*time.Minute {
		t.Fatalf("tabWriteVideo timeout = %v", timeoutFor(tabWriteVideo))
	}
}

func TestWaitForSlotBusy(t *testing.T) {
	p := &browserPool{pageSem: make(chan struct{}, maxConcurrentTabs)}
	for i := 0; i < maxConcurrentTabs; i++ {
		p.pageSem <- struct{}{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// 临时缩短等待，避免测试跑太久
	old := slotWaitTimeout
	slotWaitTimeout = 150 * time.Millisecond
	defer func() { slotWaitTimeout = old }()

	_, err := p.waitForSlot(ctx)
	if !errors.Is(err, ErrPoolBusy) {
		t.Fatalf("expected ErrPoolBusy, got %v", err)
	}
}

func TestPaceMinGap(t *testing.T) {
	// minInterOpGap/gapJitter are env-tunable vars; pin small deterministic values so the test is fast.
	restore := setPacing(t, 100*time.Millisecond, 0)
	defer restore()

	p := &browserPool{}
	p.lastPageOp = time.Now()
	start := time.Now()
	p.pace()
	if elapsed := time.Since(start); elapsed < minInterOpGap/2 {
		t.Fatalf("pace should sleep, elapsed=%v", elapsed)
	}
}

// setPacing temporarily overrides the env-tunable pacing vars for a test and returns a restore func.
func setPacing(t *testing.T, gap, jitter time.Duration) func() {
	t.Helper()
	oldGap, oldJit := minInterOpGap, gapJitter
	minInterOpGap, gapJitter = gap, jitter
	return func() { minInterOpGap, gapJitter = oldGap, oldJit }
}

// TestWaitForSlotSerializesAtOne proves the Moderate default (maxConcurrentTabs=1) is FULL
// serialization: with a 1-slot semaphore only one op holds a slot at a time, a second acquire blocks
// (then fails soft with ErrPoolBusy — never a hang), and releasing frees the lane again.
func TestWaitForSlotSerializesAtOne(t *testing.T) {
	p := &browserPool{pageSem: make(chan struct{}, 1)}
	ctx := context.Background()

	if _, err := p.waitForSlot(ctx); err != nil {
		t.Fatalf("first slot should acquire, got %v", err)
	}

	old := slotWaitTimeout
	slotWaitTimeout = 100 * time.Millisecond
	defer func() { slotWaitTimeout = old }()

	// second acquire must NOT run concurrently — it blocks then fails soft (no second lane, no hang)
	if _, err := p.waitForSlot(ctx); !errors.Is(err, ErrPoolBusy) {
		t.Fatalf("second concurrent slot must block→ErrPoolBusy at cap=1, got %v", err)
	}

	p.releaseSlot()
	if _, err := p.waitForSlot(ctx); err != nil {
		t.Fatalf("after release the lane should free up, got %v", err)
	}
}

// TestPaceJitterSpacing proves the cadence is (a) never below the base floor and (b) bounded by
// base+jitter — i.e. slower than the old fixed 800ms and randomized rather than a fixed metronome.
func TestPaceJitterSpacing(t *testing.T) {
	base, jitter := 50*time.Millisecond, 60*time.Millisecond
	restore := setPacing(t, base, jitter)
	defer restore()

	p := &browserPool{}
	p.pace() // establish lastPageOp baseline

	const slack = 120 * time.Millisecond // scheduling slack so a loaded CI machine doesn't flake
	for i := 0; i < 6; i++ {
		start := time.Now()
		p.pace()
		g := time.Since(start)
		if g < base {
			t.Fatalf("gap %d = %v is below the base floor %v", i, g, base)
		}
		if g > base+jitter+slack {
			t.Fatalf("gap %d = %v exceeds base+jitter (%v) + slack", i, g, base+jitter)
		}
	}
}

// TestCallWithTimeout is the deterministic proof of the A2 hang fix: a wedged page-open must return a
// bounded ErrTabTimeout (not block forever), a fast call must pass its result/error through, and a
// panic must be recovered into an error rather than crashing the process. No real browser involved —
// this is the risk-control-noise-free test the other pacing tests aspire to.
func TestCallWithTimeout(t *testing.T) {
	// fast success passes through
	if _, err := callWithTimeout(200*time.Millisecond, func() (*rod.Page, error) {
		return nil, nil
	}); err != nil {
		t.Fatalf("fast success: unexpected err %v", err)
	}

	// fast error passes through unchanged
	sentinel := errors.New("boom")
	if _, err := callWithTimeout(200*time.Millisecond, func() (*rod.Page, error) {
		return nil, sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("error passthrough: got %v, want %v", err, sentinel)
	}

	// a wedged fn returns a bounded ErrTabTimeout, roughly at the deadline (not after fn's 2s)
	start := time.Now()
	_, err := callWithTimeout(100*time.Millisecond, func() (*rod.Page, error) {
		time.Sleep(2 * time.Second)
		return nil, nil
	})
	if !isTabTimeoutErr(err) {
		t.Fatalf("wedged fn: want tab-timeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 800*time.Millisecond {
		t.Fatalf("wedged fn: should return ~at the 100ms deadline, took %v (blocked on fn)", elapsed)
	}

	// a panic in fn is recovered into an error (no crash)
	if _, err := callWithTimeout(200*time.Millisecond, func() (*rod.Page, error) {
		panic("kaboom")
	}); err == nil {
		t.Fatalf("panic fn: want recovered error, got nil")
	}
}
