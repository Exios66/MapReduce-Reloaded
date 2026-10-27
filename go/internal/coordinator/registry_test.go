package coordinator

import (
	"slices"
	"testing"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

func TestRegistry(t *testing.T) {
	t.Parallel()
	r := NewRegistry("w")
	t0 := time.Unix(0, 0)
	a := r.Register(&pb.WorkerInfo{ShuffleAddress: "a:1", Slots: 2}, t0)
	b := r.Register(&pb.WorkerInfo{ShuffleAddress: "b:1"}, t0)
	if a == b || r.Len() != 2 {
		t.Fatalf("IDs %q, %q; %d workers", a, b, r.Len())
	}
	if w, ok := r.Lookup(a); !ok || w.Info.GetSlots() != 2 {
		t.Fatalf("Lookup(%q) = %+v, %v", a, w, ok)
	}

	if !r.Heartbeat(a, t0.Add(10*time.Second)) {
		t.Fatal("heartbeat from a live worker rejected")
	}
	if got := r.Expire(t0.Add(15*time.Second), 10*time.Second); !slices.Equal(got, []string{b}) {
		t.Fatalf("Expire = %v, want [%s]", got, b)
	}
	if r.Heartbeat(b, t0.Add(16*time.Second)) {
		t.Fatal("heartbeat from an expired worker accepted")
	}
	if _, ok := r.Lookup(b); ok {
		t.Fatal("expired worker still listed")
	}
	if c := r.Register(&pb.WorkerInfo{ShuffleAddress: "b:1"}, t0); c == b {
		t.Fatal("a returning worker got its old ID back")
	}
}
