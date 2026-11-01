package fetchgate

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// An idle gate must not delay the single caller: the dashboard's per-provider
// refresh button pays nothing, only the burst behind it is paced.
func TestGateAcquire_IdleGateReturnsImmediately(t *testing.T) {
	g := New(250*time.Millisecond, 120*time.Millisecond)

	start := time.Now()
	if err := g.Acquire(t.Context()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("idle gate delayed the first caller by %s", elapsed)
	}
}

// timerGranularity is the only slack the wall-clock tests allow. The gate
// computes every slot exactly (slot k opens k floors after the first), Go
// timers never fire before their duration on the monotonic clock, and every
// measurement below is taken from an origin recorded before the first
// reservation. Scheduler delay can only make a measured instant later, so no
// percentage tolerance is needed; the millisecond only covers platforms with a
// coarse timer clock.
const timerGranularity = time.Millisecond

// The whole point of the gate: N simultaneous callers must not start in the
// same millisecond. This is the ten-accounts-on-one-IP case from issue #30.
//
// Measuring the gap between two neighbours' return times was flaky: one
// goroutine descheduled after its grant shrinks the next measured gap even
// though the gate spaced the grants correctly (observed 35.5ms for a 40ms
// floor). Measured from one origin, the k-th caller to get through can never
// do so before k floors have passed, whatever the scheduler does: that is the
// k-th order statistic of slots that are each at least k floors apart. A burst
// fails it at k=1.
func TestGateAcquire_SpacesConcurrentCallers(t *testing.T) {
	const (
		callers = 8
		minGap  = 40 * time.Millisecond
	)
	g := New(minGap, 0)

	var (
		mu    sync.Mutex
		slots []time.Duration
		wg    sync.WaitGroup
	)
	start := time.Now()
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.Acquire(t.Context()); err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			mu.Lock()
			slots = append(slots, time.Since(start))
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(slots) != callers {
		t.Fatalf("got %d slots, want %d", len(slots), callers)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	for k, got := range slots {
		if want := time.Duration(k)*minGap - timerGranularity; got < want {
			t.Errorf("caller #%d got through %s after the burst started, want >= %s (%d floors of %s)", k, got, want+timerGranularity, k, minGap)
		}
	}
}

// The same property on a fake clock (testing/synctest): with the scheduler and
// the timers out of the picture, the grants must be exactly one floor apart.
// This is the deterministic version; any drift in the gate's arithmetic fails
// it without tolerance.
func TestGateAcquire_SpacesConcurrentCallers_FakeClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const (
			callers = 8
			minGap  = 40 * time.Millisecond
		)
		g := New(minGap, 0)
		var (
			mu    sync.Mutex
			slots []time.Duration
			wg    sync.WaitGroup
		)
		start := time.Now()
		for range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := g.Acquire(t.Context()); err != nil {
					t.Errorf("Acquire: %v", err)
					return
				}
				mu.Lock()
				slots = append(slots, time.Since(start))
				mu.Unlock()
			}()
		}
		wg.Wait()
		sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
		for k, got := range slots {
			if want := time.Duration(k) * minGap; got != want {
				t.Errorf("caller #%d got through at %s, want exactly %s", k, got, want)
			}
		}
	})
}

// Jitter must only ever add delay: the floor stays the hard guarantee. Every
// grant is measured from one origin taken before the first Acquire, so a
// descheduled goroutine can only make a slot look later, never earlier
// (measuring from the previous return under-reported it: 22ms for a 30ms
// floor on a loaded runner).
func TestGateAcquire_JitterOnlyWidensTheGap(t *testing.T) {
	const floor = 30 * time.Millisecond
	g := New(floor, floor)

	origin := time.Now()
	if err := g.Acquire(t.Context()); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	for i := range 5 {
		if err := g.Acquire(t.Context()); err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		if elapsed, want := time.Since(origin), time.Duration(i+1)*floor-timerGranularity; elapsed < want {
			t.Fatalf("slot %d opened %s after the first, want at least %s (%d x %s floor)", i, elapsed, want+timerGranularity, i+1, floor)
		}
	}
}

// On a fake clock every gap is observable exactly: it must sit inside
// [floor, floor+maxJitter], and the jitter must actually vary (a gate that
// silently dropped it would pass the wall-clock test).
func TestGateAcquire_JitterOnlyWidensTheGap_FakeClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const floor, jitter = 30 * time.Millisecond, 30 * time.Millisecond
		g := New(floor, jitter)
		if err := g.Acquire(t.Context()); err != nil {
			t.Fatalf("first Acquire: %v", err)
		}
		prev := time.Now()
		widened := 0
		for i := range 50 {
			if err := g.Acquire(t.Context()); err != nil {
				t.Fatalf("Acquire %d: %v", i, err)
			}
			now := time.Now()
			gap := now.Sub(prev)
			prev = now
			if gap < floor || gap > floor+jitter {
				t.Fatalf("gap %d = %s, want within [%s, %s]", i, gap, floor, floor+jitter)
			}
			if gap > floor {
				widened++
			}
		}
		if widened == 0 {
			t.Fatal("jitter never widened a gap in 50 slots")
		}
	})
}

// A dashboard that navigates away mid-wait must not hold a slot against the
// requests behind it, and it must see its own cancellation as an error.
func TestGateAcquire_CanceledContextReleasesItsWait(t *testing.T) {
	g := New(200*time.Millisecond, 0)

	if err := g.Acquire(t.Context()); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := g.Acquire(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire error = %v, want context.Canceled", err)
	}
}

// A zero gap is a legitimate configuration (pure jitter, no floor), and must
// not be mistaken for a misconfigured gate.
func TestGateAcquire_ZeroGapStillGates(t *testing.T) {
	g := New(0, 0)

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.Acquire(t.Context()); err != nil {
				t.Errorf("Acquire: %v", err)
			}
		}()
	}
	wg.Wait()
}
