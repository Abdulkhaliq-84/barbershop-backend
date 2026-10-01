# ADR-0029: Search by name — normalise in Go, match with trigrams, a lower threshold

- Status: Accepted · Date: 2026-10-01 · Builds on [ADR-0005](0005-postgres-postgis.md), [ADR-0027](0027-discovery-read-model.md) and [ADR-0028](0028-nearby-search.md)

## Context
M6.3 lets a customer find a shop by typing its name. Arabic is written many ways:

- hamza on alef or not (أ / ا);
- taa marbuta or haa (ة / ه);
- alef maqsura or yaa (ى / ي);
- with short vowels (حَلّاق) or without;
- stretched with tatweel (صـــالون).

People also misspell names. Owners give names in Arabic and English. The same search must
find "صالون الأناقة" whichever way either side spelled it, without a search engine (ADR-0005).

## Decision
- **One normalisation, in Go:** `domain.Normalize` drops marks (tashkeel, the dagger alef,
  separate accents) and tatweel. It folds أ إ آ ٱ → ا, ى → ي and ة → ه, and lowercases. Every
  run of non-letters becomes one space.
  - It runs on names when discovery saves a copy (`search_text`: both languages) and on every
    search.
  - It lives in Go, not SQL, so it is one function with one set of tests. A fuzz test checks,
    for any input, that it is idempotent and leaves nothing to fold.
- **Matching:** a name matches if its `search_text` contains the search (`LIKE`), or part of
  it is close to the search (pg_trgm `<%`, word similarity).
  - A partial GIN trigram index (`WHERE listed`) answers both; the plan is a `BitmapOr` of
    two scans of it.
  - Results come best match first (`word_similarity`), then by branch ID. Pages continue by
    `(score, branch ID)`, cursor kind `m`.
- **Threshold 0.5, not pg_trgm's 0.6.** One wrong or missing letter in a short Arabic word
  scores about 0.57 ("الانقه" for "الاناقه"), which 0.6 misses. The search sets
  `pg_trgm.word_similarity_threshold` with `SET LOCAL` in its own read-only transaction, so
  nothing else on the connection is affected.
- **At least two letters or digits after normalising**, at most 60 characters. A single letter
  matches nearly everything.
- **Combining:**
  - `q` alone searches every city, best match first;
  - with `city`, only that city's;
  - with a place, only matching names, still nearest first (ADR-0028);
  - `city` alone is still by name (ADR-0027).
- **The plan** (22,000 branches, a misspelled search matching 1,290): two bitmap index scans,
  a top-N sort, about 43 ms. A sequential scan takes about 220 ms. A test checks the plan uses
  the index.

## Consequences
- Copies saved before M6.3 have an empty `search_text` until their branch next changes. Nothing
  is live, so there is no backfill (ADR-0027).
- A GIN index takes new rows into a pending list first, and the planner prices that list high
  until a vacuum merges it. Autovacuum does this in a running database; the plan test vacuums
  first.
- Matching is by letters, not meaning: "حلاق" doesn't find "barber". Synonyms would be a later
  search-engine concern.
