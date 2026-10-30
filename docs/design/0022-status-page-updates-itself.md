# ADR 0022 — The status page updates itself

**Status:** accepted · **Supersedes:** [ADR 0012](0012-observability.md)'s
decision that the status page has no JavaScript, and its rejection of a page
that updates with JavaScript. The rest of ADR 0012 stands.

## Context

ADR 0012 kept the page free of scripts and refreshed it with
`<meta http-equiv="refresh">`. That reload scrolls the page to the top,
closes the open counter lists, and cannot be paused while a reader studies
a number. It also gives no sign when the coordinator has stopped answering:
the browser shows an error page, or the last page with no hint it is stale.
The course VM is offline, so the original reason, no CDN, still holds.

## Decision

1. **One inline script.** It fetches the server-rendered page every two
   seconds and replaces the `#live` region, then reopens the counter lists
   that were open. It has a pause button, and shows a notice when a fetch
   fails. The script is the same in the Go and Python pages, and a test
   compares them.
2. **No external file, ever.** The page loads no script, style, font or
   image from anywhere. Tests fail if it contains a URL, `src=`, `<link` or
   a second `<script`.
3. **The page still works without the script.** A
   `<meta http-equiv="refresh">` inside `<noscript>` reloads it as before.
4. **`/status.json`** serves the same status as data, with the same keys from
   both coordinators.

## Alternatives rejected

- **Keep the meta refresh.** It is the cost above; the script is about 30
  lines and falls back to it.
- **Build the page in the browser from `/status.json`.** It needs a second
  renderer, kept in step with the Go and Python ones. Fetching the rendered
  page keeps one renderer per language.
- **Server-sent events or WebSockets.** The page is polled by a few readers
  every two seconds; a held connection per reader and a streaming handler
  in two languages are not worth it.

## Consequences

- The "no `<script>`" test now means "exactly one inline script, nothing
  external".
- A reader whose browser blocks scripts sees the old behavior.
- The README GIFs show the new bar; they are re-recorded after the overhaul
  (#277–#279).
