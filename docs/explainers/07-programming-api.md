# Explainer — The Programming API (#7)

Decision record: [ADR 0005](../design/0005-programming-api.md).

## What it is

The only part of the system a user touches. They write two functions:

- **map**: takes one input record (for text, one line) and outputs any
  number of `(key, value)` pairs;
- **reduce**: takes one key and all of its values, and outputs the result.

Everything else (splitting, scheduling, sorting, moving data, retrying
failures) is the framework's job. That separation is the paper's main idea.

## Word count, in both languages

```python
@mapper
def wc_map(ctx, offset, line):
    for word in line.split():
        yield word, b"1"

@reducer
def wc_reduce(ctx, word, counts):
    yield word, str(sum(int(c) for c in counts)).encode()
```

The Go version (`go/pkg/api/api_test.go`) has the same shape, using
`api.MapFunc` and `api.ReduceFunc`.

## The pieces

| Piece | Purpose | Paper |
|---|---|---|
| Mapper, Reducer | The user's two functions | §2 |
| Combiner | Pre-sums on the map side; same code as a reducer | §4.3 |
| Partitioner | Picks the reducer for a key; default is the #5 hash | §4.1 |
| Context | Task ID, job params, counters, logger | §4.9 |
| Program | The user's code (map, reduce, optional combiner and partitioner) | |
| JobSpec | A Program plus inputs, output, R and formats | §3.1 |

## Likely review questions

- **Why bytes, not typed keys?** The framework sorts and hashes keys it
  doesn't understand, in two languages. Bytes need no shared type system;
  the user converts, exactly like the paper's `ParseInt`/`AsString`.
- **Why are reduce values an iterator, not a list?** A common key can have
  more values than fit in memory.
- **Why doesn't the user set M?** As in the paper, M follows from the input
  size and the split size (64 MiB by default).
- **What happens if Python code outputs a `str`?** A `TypeError` naming the
  function. Silently encoding it could send the key to a different reducer
  than Go would.
