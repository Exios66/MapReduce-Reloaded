# Explainer — The Combiner (#11)

## What it is for

Word count's map output is mostly `("the", "1")`, thousands of times. All
of it would cross the network to one reducer. A **combiner** adds those up
on the map side first, so the map task sends `("the", "5000")` once (§4.3).

It is usually the reducer itself: for word count, summing partial sums
gives the same total as summing the ones.

## Where it runs

Every time the external sort (#10) spills a sorted buffer to disk, the
combiner runs over it first, one key at a time, and its output is what gets
written. With a combiner set, the sorter always spills at the end, so every
record passes through it.

## The rules a combiner must follow

The framework may run it zero, one or many times, on any subset of a key's
values. So it must:

- be **commutative and associative** (a sum or a maximum is; an average is
  not, unless it carries a count);
- **emit only the key it was given**. Changing it would break the sorted
  order of the run, so the framework rejects it with an error;
- **leave the result unchanged**: reducing its output must give what
  reducing its input would. Its values are the reducer's input, so they must
  be in the form the reducer reads.

## How we know it works

- Word count gives **identical output** with and without the combiner,
  while the combiner run writes **fewer records and fewer bytes**.
- Applying the combiner **0, 1, 2 and 3 times** gives the same final result.
- Two counters, `combine.input.records` and `combine.output.records`, show
  the reduction; the test checks them exactly.

## Likely review questions

- **Why not also combine during the final merge?** Each run is already
  combined, and the reducer gives the same result either way. It would save
  some bytes when there are many runs, so it is a possible later
  optimisation.
- **Why check the key on every output record?** It is one byte comparison,
  and a combiner that changes keys would silently mis-sort data.
