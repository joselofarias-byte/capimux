package combohealth

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func id(s string) string { return s }

func TestOrderKeepsConfiguredOrderWithoutData(t *testing.T) {
	tr := New(nil)
	in := []string{"a/x", "b/y", "c/z"}
	if got := tr.Order(in, id); !slices.Equal(got, in) {
		t.Fatalf("orden = %v", got)
	}
	if got := tr.Order(in, func(string) string { return "" }); !slices.Equal(got, in) {
		t.Fatalf("sin claves = %v", got)
	}
}

func TestFailingMemberMovesLastButIsKept(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	tr := New(c.now)
	for i := 0; i < 3; i++ {
		tr.Failure("a/x")
	}
	tr.Success("b/y", 300*time.Millisecond)
	got := tr.Order([]string{"a/x", "b/y", "c/z"}, id)
	if !slices.Equal(got, []string{"b/y", "c/z", "a/x"}) {
		t.Fatalf("orden = %v", got)
	}
	if len(got) != 3 {
		t.Fatal("se descartó un miembro")
	}
	snap := tr.Snapshot()
	if !snap[0].Open || snap[0].ConsecFails != 3 {
		t.Fatalf("snapshot = %+v", snap[0])
	}
	// Circuit closes after the cooldown, but the low success score still
	// keeps it behind the healthy members until it recovers.
	c.add(31 * time.Second)
	if tr.Snapshot()[0].Open {
		t.Fatal("el circuito no se cerró tras el enfriamiento")
	}
	if got := tr.Order([]string{"a/x", "b/y"}, id); got[0] != "b/y" {
		t.Fatalf("tras enfriamiento = %v", got)
	}
	for i := 0; i < 8; i++ {
		tr.Success("a/x", 100*time.Millisecond)
	}
	if got := tr.Order([]string{"b/y", "a/x"}, id); got[0] != "a/x" {
		t.Fatalf("recuperado y más rápido debería ir primero: %v", got)
	}
}

func TestCooldownBacksOff(t *testing.T) {
	if cooldown(3) != 30*time.Second || cooldown(4) != time.Minute || cooldown(20) != maxCooldown {
		t.Fatalf("cooldown: %v %v %v", cooldown(3), cooldown(4), cooldown(20))
	}
}

func TestSingleFailureDoesNotDemoteBelowUnknown(t *testing.T) {
	tr := New(nil)
	tr.Success("a/x", time.Second)
	tr.Failure("a/x") // 0.7 → band 7 vs unknown band 10
	got := tr.Order([]string{"a/x", "b/y"}, id)
	if got[0] != "b/y" {
		t.Fatalf("un fallo reciente debería ceder el primer lugar: %v", got)
	}
}

func TestConcurrentUse(t *testing.T) {
	tr := New(nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := fmt.Sprintf("p/m%d", i%5)
			for j := 0; j < 100; j++ {
				if j%3 == 0 {
					tr.Failure(k)
				} else {
					tr.Success(k, time.Duration(j)*time.Millisecond)
				}
				_ = tr.Order([]string{"p/m0", "p/m1", "p/m2"}, id)
			}
		}(i)
	}
	wg.Wait()
	var total int64
	for _, e := range tr.Snapshot() {
		total += e.Samples
	}
	if total != 5000 {
		t.Fatalf("muestras = %d, se esperaban 5000", total)
	}
}
