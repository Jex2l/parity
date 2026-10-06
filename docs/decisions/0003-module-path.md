# 0003: Go module path

Decision: use `github.com/jex2l/parity` as the Go module path (matches the
git user `Jex2l`), even though no remote repository exists yet. This keeps
import paths stable; if the project is later pushed under a different
GitHub org/user, a single `go mod edit -module` + find/replace fixes it.
