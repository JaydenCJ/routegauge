# How routegauge clusters endpoints

routegauge turns raw URL paths into route patterns in two stages. Both
are deterministic, both run offline, and every rule below has a test for
the rule *and* for its nearest near-miss.

## Stage 1 — per-segment heuristics

Each path segment is classified in isolation. The first matching rule
wins; a segment that matches nothing stays literal.

| Rule | Matches | Becomes | Deliberately does NOT match |
|---|---|---|---|
| version | `v` + 1–3 digits: `v1`, `v2`, `V10` | *(kept literal)* | — |
| numeric | all digits: `7`, `004212` | `:id` | `v1` (version), `abc123` |
| uuid | canonical 8-4-4-4-12 hex, any case | `:uuid` | right length, wrong hyphens |
| date | `YYYY-MM-DD` with month 01–12, day 01–31 | `:date` | `1234-56-78` (order number) |
| hex hash | ≥12 hex chars with ≥1 digit: git SHAs, asset fingerprints | `:hash` | `deadbeefcafe` (no digit), short hex |
| email | contains `@` | `:email` | — |
| token | ≥16 chars of `[A-Za-z0-9_-]` with ≥3 digits: session IDs, API keys | `:token` | `internationalization` (no digits) |
| extension | classified stem + 1–5 alnum ext | e.g. `:date.csv`, `:id.json` | `report.pdf` (literal stem) |

Version segments are checked first and kept literal on purpose: `/api/v1/…`
and `/api/v2/…` are different route contracts, not the same endpoint with
a parameter.

## Stage 2 — cardinality collapse

Heuristics cannot see that `/products/blue-widget` and
`/products/red-widget` are the same endpoint — slugs look like words.
So routegauge builds a tree of the (stage-1-normalized) paths and walks
it once: any position holding **more distinct literal siblings than
`--cluster-threshold`** (default 12) is collapsed into a single `:param`
node, and the collapsed children's subtrees are merged recursively — so
`/products/<slug>/reviews` ends up as one route,
`/products/:param/reviews`.

Two properties worth knowing:

- **Distinct values, not hits.** A hot endpoint called a million times
  is one distinct value; it never collapses. Only genuine fan-out does.
- **Params don't count toward the threshold.** `/users/:id` plus the
  literals `me` and `search` is three children but only two literals —
  safely below any sane threshold, so `/users/me` keeps its name.

## Tuning

| Symptom | Fix |
|---|---|
| Slug directories survive as dozens of routes | lower `--cluster-threshold` (try 5) |
| Distinct sub-resources merged into `:param` | raise `--cluster-threshold` |
| You want raw paths, no grouping at all | `--no-cluster` |
| One noisy subtree drowns the report | `--path-prefix /api` to scope it |

The `endpoints` subcommand is the debugging view for all of this: it
prints every final pattern with its request count, the number of
distinct raw paths behind it, and up to three samples.
