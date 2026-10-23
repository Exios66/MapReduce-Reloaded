package partition

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"strconv"
	"testing"
)

const vectorsFile = "../../../testdata/hash/vectors.json"

type hashVectors struct {
	Algorithm string `json:"algorithm"`
	Vectors   []struct {
		Name       string            `json:"name"`
		KeyB64     string            `json:"key_b64"`
		Hash       string            `json:"hash"`
		Partitions map[string]uint32 `json:"partitions"`
	} `json:"vectors"`
}

// TestVectors checks the shared vectors that Python is also tested against.
func TestVectors(t *testing.T) {
	raw, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	var vs hashVectors
	if err := json.Unmarshal(raw, &vs); err != nil {
		t.Fatal(err)
	}
	if vs.Algorithm != "fnv1a64" || len(vs.Vectors) == 0 {
		t.Fatalf("unexpected vectors file: algorithm %q, %d vectors", vs.Algorithm, len(vs.Vectors))
	}

	for _, v := range vs.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			key, err := base64.StdEncoding.DecodeString(v.KeyB64)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%016x", FNV1a64(key)); got != v.Hash {
				t.Errorf("FNV1a64 = %s, want %s", got, v.Hash)
			}
			for rs, want := range v.Partitions {
				r, err := strconv.ParseUint(rs, 10, 32)
				if err != nil {
					t.Fatal(err)
				}
				if got := Partition(key, uint32(r)); got != want {
					t.Errorf("Partition(R=%d) = %d, want %d", r, got, want)
				}
			}
		})
	}
}

func TestPartitionZeroPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Partition with r = 0 did not panic")
		}
	}()
	Partition([]byte("a"), 0)
}

func TestNoAllocations(t *testing.T) {
	key := []byte("some intermediate key")
	if n := testing.AllocsPerRun(100, func() { Partition(key, 7) }); n != 0 {
		t.Fatalf("Partition allocates %v times per call, want 0", n)
	}
}

// FuzzMatchesStdlib compares the hand-written hash with Go's hash/fnv, an
// independent implementation of the same algorithm, on arbitrary keys.
func FuzzMatchesStdlib(f *testing.F) {
	f.Add([]byte(""), uint32(1))
	f.Add([]byte("foobar"), uint32(7))
	f.Add([]byte{0x00, 0xff, 0x80}, uint32(4294967295))
	f.Fuzz(func(t *testing.T, key []byte, r uint32) {
		h := fnv.New64a()
		h.Write(key)
		if got, want := FNV1a64(key), h.Sum64(); got != want {
			t.Fatalf("FNV1a64(%x) = %x, stdlib %x", key, got, want)
		}
		if r == 0 {
			return
		}
		if p := Partition(key, r); p >= r {
			t.Fatalf("Partition(%x, %d) = %d, out of range", key, r, p)
		}
	})
}

func BenchmarkPartition(b *testing.B) {
	key := []byte("an average-length word count key")
	b.SetBytes(int64(len(key)))
	for b.Loop() {
		Partition(key, 64)
	}
}
