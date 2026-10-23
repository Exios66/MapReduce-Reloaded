# Explainer — Input and Output Formats (#9)

## What it is for

A JobSpec names its input and output formats (`"text"` or `"kv"`). A format
answers three questions:

1. **How is an input file divided among map tasks?** (`Splits`)
2. **What records does one piece contain?** (`Open` in Go, `read` in Python)
3. **How are output records written?** (`Create`)

| Format | Input record | Output line | Splits |
|---|---|---|---|
| `text` | key = byte offset of the line, value = the line | `key<TAB>value\n` | every `split_size` bytes |
| `kv` | the stored key and value | an MRKV file (#8) | one per file |

## The interesting part: splitting text

Splits are plain byte ranges, so a split boundary usually lands in the
middle of a line. The rule that fixes this:

> **A line belongs to the split in which it starts.**

So a reader:

- **skips** the partial line at the start of its split, because the
  previous split owns it;
- **finishes** the last line it starts, even past the end of its split.

To tell whether its split starts exactly on a line, a reader starts reading
one byte early: if that byte is `\n`, the first line is its own.

Every line is therefore read exactly once, however the file is cut. A Go
fuzz test and a Python `hypothesis` test check this for random contents
and split sizes against a plain "split on newline" reading. Changing the
reader to start at the boundary instead of one byte early makes both fail.

## Likely review questions

- **Why is the key the byte offset?** That's the paper's choice (§2), and
  it identifies a line uniquely within its file. It is written in decimal,
  so Go and Python agree.
- **Why can't `kv` files be split?** Finding a block boundary means reading
  from the start of the file. Map output is already one file per task, so
  nothing is lost.
- **Why `\r\n` handling?** Text files from Windows end lines with `\r\n`;
  the `\r` is not part of the line.
