# Explainer — The Partition Hash (#5)

Full spec: the "Partitioning" section of [file-format.md](../file-format.md#partitioning).

## What it is for

A job has R reduce tasks. Every map task must decide, for each key it
outputs, which of the R reducers gets it. The paper's rule (§3.1, §4.1) is:

```
partition(key) = hash(key) mod R
```

The one thing that matters: **every map task must send the same key to the
same reducer.** If map task 3 sends `"apple"` to reducer 2 and map task 8
sends it to reducer 5, neither reducer sees all the `"apple"` counts, and
the word count comes out wrong without any error.

## Why we can't use the built-in hash

Go's `hash/maphash` and Python's `hash()` are **randomly seeded each time a
program starts**, on purpose, to protect hash tables against attackers who
pick keys that collide. Two workers would hash `"apple"` differently. And a
Go worker and a Python worker would never agree.

So the hash is fixed in the spec and written out by hand in both languages.

## FNV-1a in five lines

```
h = 0xcbf29ce484222325        # fixed starting value
for each byte b of the key:
    h = h XOR b               # mix the byte in
    h = h * 0x100000001b3     # spread it across all 64 bits
return h                      # keep only the low 64 bits
```

- **Why FNV-1a:** it is tiny, so it is easy to write identically in two
  languages; it is fast for short keys (about 43 ns for a 32-byte key in Go,
  with no memory allocation); and it spreads keys evenly enough.
- **It is not cryptographic.** It doesn't need to be: it only spreads keys,
  it doesn't protect secrets.
- **Keys are hashed as raw bytes.** Python rejects `str` keys, so the caller
  must choose an encoding.
- **R = 0 is an error:** Go panics (the coordinator already rejects such
  jobs), Python raises `ValueError`.

## How we know both languages agree

- `scripts/make-hash-vectors.py` computes the hash with its own copy of the
  loop, checks it against FNV-1a's published values (`""`, `"a"`,
  `"foobar"`), and writes `testdata/hash/vectors.json`: 11 keys (empty,
  accented, Japanese, emoji, zero bytes, all 256 byte values, a 1000-byte
  key), each with its hash and its reducer for six values of R.
- **Go** and **Python** are both tested against that file.
- **Go** is also fuzz-tested against the standard library's `hash/fnv`, a
  third, independent implementation, on random keys.
- **Proof the tests can fail:** changing the prime by 2 fails both the Go
  and the Python tests.

## Likely review questions

- **What about skew,** where one key (like `"the"`) has far more values
  than others? A hash can't fix that: all of one key's values must go to one
  reducer. The combiner (#11) shrinks it on the map side.
- **Can a job use its own partitioner?** Yes, the paper's example is
  `hash(hostname(url)) mod R`, so all pages of one site end up in the same
  output file. A program may set its own `Partitioner`, in Go and in
  Python; the `distsort` example does, to keep its output in global order.
  The hash is the default.
- **Why `mod R` and not something fancier, like consistent hashing?** R is
  fixed for the whole job, so nothing gets re-shuffled when workers come and
  go. Consistent hashing solves a problem we don't have.
