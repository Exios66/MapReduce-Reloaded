package contract

import (
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

// castagnoli is the CRC-32C table required by docs/file-format.md.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

const formatDir = "../../../testdata/format"

type formatVector struct {
	File    string     `json:"file"`
	Records [][]string `json:"records"`
	Error   string     `json:"error"`
}

func TestCRC32CCheckValue(t *testing.T) {
	if got := crc32.Checksum([]byte("123456789"), castagnoli); got != 0xE3069283 {
		t.Fatalf("crc32c check value = %#x, want 0xe3069283", got)
	}
}

// TestFormatVectorChecksums walks the block structure of every valid MRKV
// vector and confirms Go's CRC-32C agrees with each recorded checksum, so the
// Python-generated files are readable from Go. The full Go reader is #8.
func TestFormatVectorChecksums(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(formatDir, "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors map[string]formatVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}

	for name, v := range vectors {
		if v.Error != "" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(formatDir, v.File))
			if err != nil {
				t.Fatal(err)
			}
			if string(data[:4]) != "MRKV" || data[4] != 1 {
				t.Fatalf("bad header % x", data[:8])
			}

			le := binary.LittleEndian
			pos, blocks, records := 8, uint64(0), uint64(0)
			for {
				storedLen := int(le.Uint32(data[pos:]))
				if storedLen == 0 {
					break
				}
				count, checksum := le.Uint32(data[pos+4:]), le.Uint32(data[pos+8:])
				payload := data[pos+12 : pos+12+storedLen]
				if got := crc32.Checksum(payload, castagnoli); got != checksum {
					t.Fatalf("block at %d: crc32c %#x, file says %#x", pos, got, checksum)
				}
				blocks++
				records += uint64(count)
				pos += 12 + storedLen
			}

			totals := data[pos+4 : pos+20]
			if got := crc32.Checksum(totals, castagnoli); got != le.Uint32(data[pos+20:]) {
				t.Fatalf("footer crc32c mismatch")
			}
			if le.Uint64(totals) != records || le.Uint64(totals[8:]) != blocks {
				t.Fatalf("footer totals do not match blocks read")
			}
			if records != uint64(len(v.Records)) {
				t.Fatalf("read %d records, manifest lists %d", records, len(v.Records))
			}
			if pos+24 != len(data) {
				t.Fatalf("unexpected bytes after footer")
			}
		})
	}
}
