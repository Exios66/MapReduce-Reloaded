package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
)

func fixtureLines() [][]byte {
	var lines [][]byte
	for i, key := range []string{"bbbbbbbbbb", "a\tbcdefghi", "aaaaaaaaaa", "bbbbbbbbbb"} {
		line := bytes.Repeat([]byte{byte('a' + i)}, 99)
		copy(line, key)
		line[31], line[50], line[51] = '\t', 0, 255
		if i%2 == 1 {
			copy(line[66:], "XYZ")
		}
		lines = append(lines, line)
	}
	return lines
}

func inputBytes(lines [][]byte) []byte {
	var in bytes.Buffer
	for _, line := range lines {
		in.Write(line)
		in.WriteByte('\n')
	}
	return in.Bytes()
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeOutputs(t *testing.T, dir, kind, format string, parts int, lines [][]byte) {
	t.Helper()
	for part := 0; part < parts; part++ {
		w, err := newRecordWriter(filepath.Join(dir, fmt.Sprintf("part-r-%05d", part)), format)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range lines[part*len(lines)/parts : (part+1)*len(lines)/parts] {
			key, value := line, []byte(nil)
			if kind == "sort" {
				key, value = line[:10], line
			}
			if err := w.write(key, value); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := w.close(); err != nil {
			t.Fatal(err)
		}
	}
}

func sortedLines(lines [][]byte) [][]byte {
	result := append([][]byte(nil), lines...)
	sort.Slice(result, func(i, j int) bool { return bytes.Compare(result[i][:10], result[j][:10]) < 0 })
	return result
}

func TestPrepareRoundTrip(t *testing.T) {
	for _, format := range []string{"text", "kv"} {
		t.Run(format, func(t *testing.T) {
			lines := fixtureLines()
			dir := filepath.Join(t.TempDir(), "input")
			m, err := prepare(bytes.NewReader(inputBytes(lines)), dir, 4, 3, format)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := readManifest(filepath.Join(dir, "manifest.json"))
			if err != nil || stored != m || m.LogicalBytes != 400 || m.GrepRecords != 2 {
				t.Fatalf("manifest = %+v, %v; want %+v", stored, err, m)
			}
			var physical int64
			index := 0
			for part, count := range []int{2, 1, 1} {
				ext := ".txt"
				if format == "kv" {
					ext = ".kv"
				}
				data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("part-m-%05d%s", part, ext)))
				if err != nil {
					t.Fatal(err)
				}
				physical += int64(len(data))
				if format == "text" {
					var want bytes.Buffer
					for range count {
						want.Write(lines[index])
						want.WriteByte('\n')
						index++
					}
					if !bytes.Equal(data, want.Bytes()) {
						t.Fatalf("wrong shard %d bytes or offsets", part)
					}
				} else {
					if len(data) < 6 || data[5] != byte(kv.CodecZstd) {
						t.Fatal("input is not zstd MRKV")
					}
					r := kv.NewReader(bytes.NewReader(data))
					for range count {
						if !r.Next() || string(r.Key()) != strconv.Itoa(100*index) || !bytes.Equal(r.Value(), lines[index]) {
							t.Fatalf("wrong shard %d record %d: %v", part, index, r.Err())
						}
						index++
					}
					if r.Next() || r.Err() != nil {
						t.Fatalf("bad input footer/count: %v", r.Err())
					}
				}
			}
			if m.PhysicalBytes != physical {
				t.Fatalf("physical bytes = %d, want %d", m.PhysicalBytes, physical)
			}
			for _, kind := range []string{"sort", "grep"} {
				out := t.TempDir()
				selected := sortedLines(lines)
				if kind == "grep" {
					selected = [][]byte{lines[3], lines[1]} // Grep has no global order requirement.
				}
				writeOutputs(t, out, kind, format, 2, selected)
				summary, err := verify(out, kind, format, 2, m)
				if err != nil || !summary.Verified || summary.Records != int64(len(selected)) || summary.PhysicalOutputBytes <= 0 {
					t.Fatalf("%s verification = %+v, %v", kind, summary, err)
				}
				var stdout bytes.Buffer
				if err := run([]string{"verify", "-kind", kind, "-format", format, "-parts", "2", "-manifest", filepath.Join(dir, "manifest.json"), out}, nil, &stdout); err != nil {
					t.Fatal(err)
				}
				var decoded verification
				if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil || decoded != summary {
					t.Fatalf("JSON summary: %s, %v", stdout.Bytes(), err)
				}
			}
		})
	}
}

func TestPrepareRejectsBadInputAndPreservesExistingDirectory(t *testing.T) {
	valid := inputBytes(fixtureLines())
	for _, format := range []string{"text", "kv"} {
		for _, problem := range []string{"short", "truncated", "extra-record", "trailing-byte", "LF", "CR", "no-final-LF"} {
			t.Run(format+"/"+problem, func(t *testing.T) {
				data := append([]byte(nil), valid...)
				switch problem {
				case "short":
					data = data[:300]
				case "truncated":
					data = data[:350]
				case "extra-record":
					data = append(data, valid[:100]...)
				case "trailing-byte":
					data = append(data, 'x')
				case "LF":
					data[23] = '\n'
				case "CR":
					data[123] = '\r'
				case "no-final-LF":
					data[399] = 'x'
				}
				dir := filepath.Join(t.TempDir(), "input")
				if _, err := prepare(bytes.NewReader(data), dir, 4, 3, format); err == nil {
					t.Fatal("accepted bad input")
				}
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("failed preparation left its directory: %v", err)
				}
			})
		}
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "keep")
	mustWrite(t, marker, []byte("untouched"))
	if _, err := prepare(bytes.NewReader(valid), dir, 4, 3, "text"); err == nil {
		t.Fatal("accepted existing directory")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "untouched" {
		t.Fatalf("existing directory was changed: %q, %v", data, err)
	}
}

func TestVerifyRejectsInvalidOutputs(t *testing.T) {
	lines := fixtureLines()
	m, err := prepare(bytes.NewReader(inputBytes(lines)), filepath.Join(t.TempDir(), "in"), 4, 2, "text")
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"text", "kv"} {
		for _, problem := range []string{"missing-part", "extra-part", "wrong-name", "corrupt", "truncated", "trailing", "missing-record", "duplicate-record", "unsorted-within", "unsorted-across", "wrong-key", "grep-no-pattern", "grep-nonempty-value"} {
			t.Run(format+"/"+problem, func(t *testing.T) {
				out := t.TempDir()
				selected := sortedLines(lines)
				kind := "sort"
				switch problem {
				case "missing-record":
					selected = selected[:3]
				case "duplicate-record":
					selected[3] = selected[2] // Same key/count, different multiset.
				case "unsorted-within":
					selected[0], selected[1] = selected[1], selected[0]
				case "unsorted-across":
					selected = append(selected[2:], selected[:2]...)
				case "grep-no-pattern":
					kind, selected = "grep", [][]byte{lines[0], lines[1]}
				case "grep-nonempty-value":
					kind, selected = "grep", [][]byte{lines[1], lines[3]}
				}
				writeOutputs(t, out, kind, format, 2, selected)
				path := filepath.Join(out, "part-r-00000")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				switch problem {
				case "missing-part":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				case "extra-part":
					mustWrite(t, filepath.Join(out, "part-r-00002"), nil)
				case "wrong-name":
					mustWrite(t, filepath.Join(out, "part-r-0"), nil)
				case "corrupt":
					data[len(data)/2] ^= 1
					mustWrite(t, path, data)
				case "truncated":
					mustWrite(t, path, data[:len(data)-1])
				case "trailing":
					mustWrite(t, path, append(data, 'x'))
				case "wrong-key", "grep-nonempty-value":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					w, err := newRecordWriter(path, format)
					if err != nil {
						t.Fatal(err)
					}
					key, value := []byte("0000000000"), selected[0]
					if kind == "grep" {
						key, value = selected[0], []byte("unexpected")
					}
					if err := w.write(key, value); err != nil {
						t.Fatal(err)
					}
					if _, err := w.close(); err != nil {
						t.Fatal(err)
					}
				}
				if summary, err := verify(out, kind, format, 2, m); err == nil || summary.Verified {
					t.Fatalf("accepted %s: %+v, %v", problem, summary, err)
				}
			})
		}
	}
}

func TestFingerprint(t *testing.T) {
	lines := fixtureLines()
	var f fingerprint
	want := new(big.Int)
	for i := 0; i < 257; i++ {
		line := lines[i%len(lines)]
		f.add(line)
		h := sha256.Sum256(line)
		want.Add(want, new(big.Int).SetBytes(h[:]))
	}
	want.Mod(want, new(big.Int).Lsh(big.NewInt(1), 256))
	hash := fmt.Sprintf("%064x", want)
	if !f.matches(257, hash) || f.matches(256, hash) || f.matches(257, strings.Repeat("0", 64)) {
		t.Fatalf("fingerprint = %s / %d, want %s / 257", hex.EncodeToString(f.sum[:]), f.count, hash)
	}
}

func TestEmptyGrepAndCLIValidation(t *testing.T) {
	line := bytes.Repeat([]byte("a"), 99)
	dir := filepath.Join(t.TempDir(), "input")
	var stdout bytes.Buffer
	if err := run([]string{"prepare", "-records", "1", "-shards", "1", "-format", "text", dir}, bytes.NewReader(inputBytes([][]byte{line})), &stdout); err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(stdout.Bytes(), &m); err != nil || m.Records != 1 {
		t.Fatalf("prepare JSON: %s, %v", stdout.Bytes(), err)
	}
	for _, format := range []string{"text", "kv"} {
		out := t.TempDir()
		writeOutputs(t, out, "grep", format, 1, nil)
		if summary, err := verify(out, "grep", format, 1, m); err != nil || !summary.Verified || summary.Records != 0 {
			t.Fatalf("empty grep = %+v, %v", summary, err)
		}
	}
	for _, args := range [][]string{nil, {"unknown"}, {"prepare"}, {"prepare", "-bad"}, {"prepare", "-records", "0", dir}, {"prepare", "-records", "1", "-shards", "2", dir}, {"prepare", "-format", "bad", dir}, {"verify"}, {"verify", "-manifest", "missing", dir}} {
		if err := run(args, bytes.NewReader(nil), io.Discard); err == nil {
			t.Fatalf("accepted invalid CLI: %v", args)
		}
	}
}
