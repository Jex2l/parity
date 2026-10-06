## Parity / behavioral diff

Replayed **500** requests. Fidelity: **97%**.

**1 cluster(s)** found (3 signal change(s), 42 absorbed as noise).


<details>
<summary>1. [HIGH] /orders/{id} — LIKELY_UNINTENDED (5 occurrence(s))</summary>

- **Changed paths:** `$.status`
- **Change types:** FIELD_REMOVED, FIELD_ADDED
- **Blast radius:** 5.0 weighted request(s)
- **Why LIKELY_UNINTENDED (80% confidence):** a field was removed in code this PR did not touch
- **Example exchange(s):** `ex-1`, `ex-2`

</details>

### Performance regressions

| Endpoint | Base p95 | Candidate p95 | Effect | p-value |
|---|---|---|---|---|
| /slow | 50.2ms | 253.8ms | +405% | 6.30e-08 |

### Generated tests

https://example.invalid/tests/run-123
