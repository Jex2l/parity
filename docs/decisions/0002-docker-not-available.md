# 0002: Docker daemon unavailable in this dev environment

Context: `docker info` fails in this environment (daemon not running).
Phase 2+ requires Docker Compose environment provisioning for replay, and
some Phase 1 integration tests would ideally use testcontainers.

Decision: Phase 1 avoids any hard dependency on a running Docker daemon.
Integration-style tests that would need containers are written using
`httptest` servers and in-process fakes instead. Any test that truly
requires Docker checks for the daemon and calls `t.Skip` with a clear
message rather than failing CI or faking success. Phase 2 replay work will
re-check Docker availability before claiming Compose-based replay works,
and the phase summary will state plainly if it could not be verified
end-to-end in this environment.

Consequences: some acceptance criteria that depend on container
orchestration may be verified only partially (code review + unit tests)
until a Docker daemon is available to run them live.
