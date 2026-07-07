package main

import (
	"context"
	"errors"
	"testing"
	"time"
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
	p := &browserPool{}
	p.lastPageOp = time.Now()
	start := time.Now()
	p.pace()
	if elapsed := time.Since(start); elapsed < minInterOpGap/2 {
		t.Fatalf("pace should sleep, elapsed=%v", elapsed)
	}
}
