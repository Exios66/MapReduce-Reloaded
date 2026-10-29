// Command paper-data streams the original Python benchmark generators' output
// into input shards, or verifies benchmark output without retaining the dataset.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
)

type manifest struct {
	Records         int64  `json:"records"`
	LogicalBytes    int64  `json:"logical_bytes"`
	PhysicalBytes   int64  `json:"physical_bytes"`
	Shards          int    `json:"shards"`
	Format          string `json:"format"`
	SortFingerprint string `json:"sort_fingerprint"`
	GrepFingerprint string `json:"grep_fingerprint"`
	GrepRecords     int64  `json:"grep_records"`
}

// fingerprint is the big-endian sum of SHA-256(line) modulo 2^256, plus
// a separately checked count. This is probabilistic multiset verification,
// not a collision-free proof; ordering is checked independently for sort.
type fingerprint struct {
	sum   [sha256.Size]byte
	count int64
}

func (f *fingerprint) add(line []byte) {
	h := sha256.Sum256(line)
	carry := uint16(0)
	for i := len(h) - 1; i >= 0; i-- {
		carry += uint16(f.sum[i]) + uint16(h[i])
		f.sum[i] = byte(carry)
		carry >>= 8
	}
	f.count++
}

func (f *fingerprint) matches(count int64, hash string) bool {
	return f.count == count && hex.EncodeToString(f.sum[:]) == hash
}

func validLine(line []byte) bool {
	return len(line) == 99 && !bytes.ContainsAny(line, "\r\n")
}

func validFormat(format string) bool { return format == "text" || format == "kv" }

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "paper-data:", err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("expected prepare or verify")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("format", "text", "text (none) or kv (zstd)")
	switch args[0] {
	case "prepare":
		records := flags.Int64("records", 100_000_000, "number of 100-byte stdin records")
		shards := flags.Int("shards", 160, "input shards")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return errors.New("usage: paper-data prepare -records N -shards M -format text|kv DIR")
		}
		m, err := prepare(in, flags.Arg(0), *records, *shards, *format)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(m)
	case "verify":
		kind := flags.String("kind", "", "grep or sort")
		parts := flags.Int("parts", 1, "expected reduce parts")
		path := flags.String("manifest", "", "input manifest.json")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 1 || *path == "" {
			return errors.New("usage: paper-data verify -kind grep|sort -format text|kv -parts R -manifest FILE OUTDIR")
		}
		m, err := readManifest(*path)
		if err != nil {
			return err
		}
		summary, err := verify(flags.Arg(0), *kind, *format, *parts, m)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(summary)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

type recordWriter struct {
	file *os.File
	buf  *bufio.Writer
	kv   *kv.Writer
}

func newRecordWriter(path, format string) (*recordWriter, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	w := &recordWriter{file: f, buf: bufio.NewWriterSize(f, 64<<10)}
	if format == "kv" {
		w.kv = kv.NewWriterCodec(w.buf, kv.CodecZstd)
	}
	return w, nil
}

func (w *recordWriter) write(key, value []byte) error {
	if w.kv != nil {
		return w.kv.Write(key, value)
	}
	for _, field := range [][]byte{key, {'\t'}, value, {'\n'}} {
		if _, err := w.buf.Write(field); err != nil {
			return err
		}
	}
	return nil
}

func (w *recordWriter) close() (int64, error) {
	var err error
	if w.kv != nil {
		err = w.kv.Close()
	}
	err = errors.Join(err, w.buf.Flush())
	info, statErr := w.file.Stat()
	err = errors.Join(err, statErr, w.file.Close())
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func prepare(in io.Reader, dir string, records int64, shards int, format string) (m manifest, err error) {
	if records <= 0 || records > math.MaxInt64/100 || shards <= 0 || int64(shards) > records || !validFormat(format) {
		return m, errors.New("require 0 < shards <= records <= MaxInt64/100 and format text|kv")
	}
	if err = os.Mkdir(dir, 0o755); err != nil {
		return m, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(dir)) // Only the directory this invocation created.
		}
	}()
	m = manifest{Records: records, LogicalBytes: 100 * records, Shards: shards, Format: format}
	r := bufio.NewReaderSize(in, 64<<10)
	var all, grep fingerprint
	var index int64
	ext := ".txt"
	if format == "kv" {
		ext = ".kv"
	}
	for shard := 0; shard < shards; shard++ {
		count := records / int64(shards)
		if int64(shard) < records%int64(shards) {
			count++
		}
		var size int64
		size, err = prepareShard(r, filepath.Join(dir, fmt.Sprintf("part-m-%05d%s", shard, ext)), format, index, count, &all, &grep)
		if err != nil {
			return m, err
		}
		m.PhysicalBytes += size
		index += count
	}
	if _, err = r.ReadByte(); err != io.EOF {
		if err == nil {
			err = errors.New("extra input after expected records")
		}
		return m, err
	}
	err = nil
	m.SortFingerprint = hex.EncodeToString(all.sum[:])
	m.GrepFingerprint = hex.EncodeToString(grep.sum[:])
	m.GrepRecords = grep.count
	f, err := os.OpenFile(filepath.Join(dir, "manifest.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return m, err
	}
	err = errors.Join(json.NewEncoder(f).Encode(m), f.Close())
	return m, err
}

func prepareShard(in io.Reader, path, format string, start, count int64, all, grep *fingerprint) (size int64, err error) {
	w, err := newRecordWriter(path, format)
	if err != nil {
		return 0, err
	}
	defer func() {
		var closeErr error
		size, closeErr = w.close()
		err = errors.Join(err, closeErr)
	}()
	var record [100]byte
	var key [20]byte
	for i := int64(0); i < count; i++ {
		if _, err = io.ReadFull(in, record[:]); err != nil {
			return 0, fmt.Errorf("input record %d: %w", start+i, err)
		}
		line := record[:99]
		if record[99] != '\n' || !validLine(line) {
			return 0, fmt.Errorf("input record %d: require 99 bytes without CR/LF followed by LF", start+i)
		}
		if format == "text" {
			_, err = w.buf.Write(record[:])
		} else {
			err = w.write(strconv.AppendInt(key[:0], 100*(start+i), 10), line)
		}
		if err != nil {
			return 0, err
		}
		all.add(line)
		if bytes.Contains(line, []byte("XYZ")) {
			grep.add(line)
		}
	}
	return 0, nil
}

func readManifest(path string) (m manifest, err error) {
	f, err := os.Open(path)
	if err != nil {
		return m, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return m, err
	}
	if info.Size() > 16<<10 {
		return m, errors.New("manifest exceeds 16 KiB")
	}
	d := json.NewDecoder(io.LimitReader(f, 16<<10))
	d.DisallowUnknownFields()
	if err = d.Decode(&m); err != nil {
		return m, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return m, errors.New("trailing manifest data")
	}
	err = nil
	if m.Records <= 0 || m.Records > math.MaxInt64/100 || m.LogicalBytes != 100*m.Records || m.PhysicalBytes <= 0 || m.Shards <= 0 || int64(m.Shards) > m.Records || !validFormat(m.Format) || m.GrepRecords < 0 || m.GrepRecords > m.Records {
		return m, errors.New("invalid manifest fields")
	}
	for _, hash := range []string{m.SortFingerprint, m.GrepFingerprint} {
		b, e := hex.DecodeString(hash)
		if e != nil || len(b) != sha256.Size || hash != strings.ToLower(hash) {
			return m, errors.New("invalid manifest fingerprint")
		}
	}
	return m, nil
}

type verification struct {
	Verified            bool  `json:"verified"`
	Records             int64 `json:"records"`
	PhysicalOutputBytes int64 `json:"physical_output_bytes"`
}

func verify(dir, kind, format string, parts int, m manifest) (summary verification, err error) {
	if (kind != "sort" && kind != "grep") || !validFormat(format) || parts <= 0 {
		return summary, errors.New("require kind grep|sort, format text|kv, and positive parts")
	}
	if err = checkParts(dir, parts); err != nil {
		return summary, err
	}
	wantCount, wantHash := m.Records, m.SortFingerprint
	if kind == "grep" {
		wantCount, wantHash = m.GrepRecords, m.GrepFingerprint
	}
	var got fingerprint
	var previous [10]byte
	visit := func(key, value []byte) error {
		line := key
		if kind == "sort" {
			line = value
			if len(key) != 10 || !validLine(line) || !bytes.Equal(key, line[:10]) {
				return errors.New("invalid sort key/value")
			}
			if got.count > 0 && bytes.Compare(previous[:], key) > 0 {
				return errors.New("sort output is not globally nondecreasing")
			}
			copy(previous[:], key)
		} else if len(value) != 0 || !validLine(line) || !bytes.Contains(line, []byte("XYZ")) {
			return errors.New("invalid grep key/value or missing XYZ")
		}
		got.add(line)
		if got.count > wantCount {
			return errors.New("too many output records")
		}
		return nil
	}
	for part := 0; part < parts; part++ {
		var size int64
		size, err = verifyPart(filepath.Join(dir, fmt.Sprintf("part-r-%05d", part)), kind, format, visit)
		if err != nil {
			return summary, err
		}
		summary.PhysicalOutputBytes += size
	}
	if !got.matches(wantCount, wantHash) {
		return summary, fmt.Errorf("output count/fingerprint mismatch: got %d records, want %d", got.count, wantCount)
	}
	summary.Verified, summary.Records = true, got.count
	return summary, nil
}

func checkParts(dir string, parts int) (err error) {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	for {
		entries, e := d.ReadDir(128)
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, "part-") {
				continue
			}
			n, parseErr := strconv.Atoi(strings.TrimPrefix(name, "part-r-"))
			if parseErr != nil || n < 0 || n >= parts || name != fmt.Sprintf("part-r-%05d", n) || !entry.Type().IsRegular() {
				return fmt.Errorf("unexpected output part %q", name)
			}
		}
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
	}
}

func verifyPart(path, kind, format string, visit func([]byte, []byte) error) (size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("not a regular output file: %s", path)
	}
	if format == "kv" {
		r := kv.NewReader(f)
		for r.Next() {
			if err = visit(r.Key(), r.Value()); err != nil {
				return 0, fmt.Errorf("%s: %w", path, err)
			}
		}
		return info.Size(), r.Err() // Reading to EOF validates the MRKV footer.
	}
	r := bufio.NewReaderSize(f, 64<<10)
	var record [111]byte
	width, keySize := 101, 99
	if kind == "sort" {
		width, keySize = 111, 10
	}
	for {
		_, err = io.ReadFull(r, record[:width])
		if err == io.EOF {
			return info.Size(), nil
		}
		if err != nil {
			return 0, fmt.Errorf("%s: %w", path, err)
		}
		if record[keySize] != '\t' || record[width-1] != '\n' {
			return 0, fmt.Errorf("%s: malformed text output record", path)
		}
		if err = visit(record[:keySize], record[keySize+1:width-1]); err != nil {
			return 0, fmt.Errorf("%s: %w", path, err)
		}
	}
}
