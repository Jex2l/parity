# 0004: Local PostgreSQL for the Phase 6 spike, not Docker

Context: Phase 6's stateful-fidelity spike needs a real database to
record query/result pairs against and prove the fidelity claim with
actual numbers. ADR 0002 already established that Docker is unavailable
in this dev environment.

Decision: install PostgreSQL 16 directly via Homebrew
(`brew install postgresql@16`, `brew services start postgresql@16`)
rather than attempting a containerized database. This matches the
project's "prefer boring, well-understood technology" principle and
needed no new infrastructure beyond what the host already had available
(Homebrew). Tests connect via `PARITY_TEST_DSN` (default
`postgres://localhost:5432/parity_test?sslmode=disable`) and skip
(`t.Skip`, not fail) when no Postgres is reachable, so `make test`
degrades gracefully on a machine without this local Postgres service
running — consistent with how ADR 0002 handles the Docker-unavailable
case elsewhere in the repo.

Consequences: anyone running `internal/dbrecord`'s tests needs either a
local Postgres matching the default DSN, or to set `PARITY_TEST_DSN`
themselves, or they'll see those specific tests skipped (not failed).
