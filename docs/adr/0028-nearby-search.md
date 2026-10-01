# ADR-0028: Nearby search — a generated geography column, one distance everywhere, pages by distance

- Status: Accepted · Date: 2026-10-01 · Builds on [ADR-0005](0005-postgres-postgis.md) and [ADR-0027](0027-discovery-read-model.md)

## Context
M6.2 answers the app's first question: which barbershops are near me? A customer sends a point
(their location) and gets the published branches around it, nearest first, with the distance,
a page at a time. Four questions:

- how the location is stored;
- which distance to use (PostGIS has two);
- how far to look;
- how to page through results sorted by a number computed per search.

## Decision
- **Stored:** `location geography(Point, 4326)`, a column the database generates from
  `latitude` and `longitude`, which discovery already keeps (migration 00022).
  - Go and the upsert still write plain numbers; PostGIS keeps the geography in step.
  - A partial GiST index (`WHERE listed`) covers only what is searched.
- **One distance, on a sphere.** PostGIS measures geography on the spheroid by default
  (`ST_Distance`, `ST_DWithin`), but on a sphere for `<->`. The search uses the sphere
  everywhere: `ST_DWithin(…, false)` for the radius, and `<->` for the order, the distance
  shown and the cursor. So a branch never sorts by one number and pages by another. The
  sphere is at most about 0.3 % off, a few metres in a city.
- **Radius:** `radius_km` from 1 to 50, 10 if not given. `city` may narrow it to one city's
  branches. `lat` and `lng` come together. Without them, `city` is required, and branches
  are listed by name (ADR-0027).
- **Pages by distance:** a keyset cursor of `(distance, branch ID)`. The distance is written
  in full (`strconv.FormatFloat(d, 'g', -1, 64)`), so it reads back exactly, and
  `(location <-> point, branch_id) > (d, id)` starts exactly after the last branch shown,
  even between two branches at the same place.
  - A cursor says which kind of search made it (`n|…` by name, `d|…` by distance); one
    from the other kind is refused (`400`).
- **The plan:** the radius makes the GiST index the way in.
  - 22,000 branches: a bitmap index scan finds the ~800 inside the radius's box. The exact
    check keeps ~560, and a top-N sort returns 21. About 13 ms.
  - Without the index: a sequential scan computing the distance of every branch, about 60 ms.
    It grows with every branch on the platform; the index scan grows only with the branches
    near the point.
  - A test checks the plan still uses the index.

## Consequences
- The distance is straight-line, not by road: the app says "1.2 km away", not "4 minutes".
- Deep pages re-scan from the start of the radius. With at most a few hundred branches inside
  50 km that is cheap. A city with thousands would want a tighter radius first.
- The coordinates customers send are search input only: not stored, logged or put in errors.
