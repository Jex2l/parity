# Parity Labs: a trust layer for AI agents in production

**Status: pillar 1 has working code in this repo (`cmd/parity-ci`,
`action.yml`, phase 8). Pillars 2 and 3 are design sketches, not code —
marked explicitly below. Nothing here should be read as "built" unless
this document says so.**

## The problem

AI agents are increasingly trusted to write code, clean data, retrain
models, and triage incidents — mostly unsupervised, because reviewing
everything by hand erases the productivity gain. The one question every
team adopting this hits, and currently solves with ad-hoc glue code, is:
**how do we know an agent's proposed action is safe, and its reasoning
is actually true, before it's allowed to act?**

Three independent prototypes already answer three different slices of
that question:

| Slice of the problem | Prototype | What it actually does |
|---|---|---|
| "Is this *change* safe?" | `parity` (this repo) | Replays real production traffic against a candidate build and reports what actually changed in behavior, not what unit tests happened to check. |
| "Is this *claim* true?" | `meridian-legal-intelligence` | Every generated answer must cite evidence actually retrieved for the query; an unsupported claim is rejected rather than shown; a faithfulness check catches citations that exist but don't say what's claimed. |
| "Is this *action* authorized?" | `incident-agent` | Read-only investigation is unrestricted; any write requires explicit human approval through a separate, infrastructure-enforced gate — not a prompt asking the model to behave. |

Parity Labs is the idea of productizing these three patterns as one
platform, instead of three unrelated side projects each solving the same
underlying problem for a different domain.

## Pillar 1 — replay verification (built, this repo)

**Status: real.** `parity replay` (phase 2) does the core work; `parity-ci`
(phase 8) is the packaging that turns a `report.json` into a GitHub
check run and PR comment via `action.yml`, so a repo adopts this by
adding one workflow step, not by writing integration code.

This is the wedge: it's useful standalone, for exactly one use case
("CI for AI-written code"), with no dependency on the other two pillars.
See `docs/plans/phase-8-parity-ci.md` for what's built and what isn't.

```
PR opened/updated
       │
       ▼
┌─────────────────┐     ┌──────────────────┐
│ build base (main)│     │ build candidate   │
│       build       │     │     (PR)          │
└────────┬─────────┘     └─────────┬─────────┘
         │                          │
         └──────────┬───────────────┘
                     ▼
         replay captured traffic
       against both (internal/orchestrate)
                     │
                     ▼
         diff + cluster + classify
     (internal/diff, internal/classify)
                     │
                     ▼
              report.json
                     │
                     ▼
         parity-ci: render + post
       (check run + PR comment, internal/gh)
```

## Pillar 2 — grounded reasoning (sketch, no code)

**Status: not built.** The idea: wrap `meridian-legal-intelligence`'s
retrieval→generate→citation-check→faithfulness-check pipeline as a
reusable SDK any agent can call before stating a claim — "the logs show
X" or "this clause means Y" use the identical verification mechanism,
only the document corpus changes (log lines and runbooks instead of case
law and contracts).

Sketched integration point for pillar 1: when `parity-ci` classifies a
cluster, the heuristic classifier (`internal/classify.Heuristic`) is a
structural rule-of-thumb, explicitly built as "the baseline that must
exist before any LLM-based classifier" (see that package's doc comment).
A grounded LLM classifier — one that cites the actual changed JSON paths
and the actual PR diff hunks it was given, with a faithfulness check
before it's allowed to assert "this is LIKELY_INTENDED" — is the natural
second implementation of the existing `classify.Classifier` interface.
Nothing about pillar 1's code needs to change to support this; it's
additive.

## Pillar 3 — approval-gated execution (sketch, no code)

**Status: not built.** The idea: `incident-agent`'s pattern — unrestricted
read/investigate, human-approval-gated write, enforced by infrastructure
(a separate executor service with its own credentials) rather than by
asking the model nicely — generalizes past incident response. Applied to
pillar 1: a `parity-ci` run that finds unintended diffs could, instead of
just failing a check, let an agent propose an auto-fix, which itself
gets replayed (pillar 1, recursively) and then requires the same
human-click approval gate `incident-agent`'s executor already implements
before it's allowed to push a commit.

## Why these three and not a generic "agent framework"

Each pillar maps to a concrete, previously-solved technical problem with
working code behind it, not a hand-wave. The discipline of only writing
this document after `cmd/parity-ci` actually existed and passed real
tests (not before) is intentional — see `docs/plans/phase-8-parity-ci.md`
for exactly what's real versus what's still an idea, consistent with how
every other phase in this repo is documented.

## What a real first customer would need, roughly in order

1. Pillar 1 works for them standalone (today's `action.yml`) — this is
   the actual thing to pitch and sell first; it needs zero code from
   pillars 2/3.
2. A hosted version of `parity-webui` (phase 7) per-customer, instead of
   local-only — multi-repo, multi-run history, access control. Not
   started; phase 7's doc already flags "no auth on the server" as a
   known gap for exactly this reason.
3. Pillar 2's grounded classifier, once there's a customer whose false
   positive/negative rate on the heuristic classifier is the actual
   blocker to wider adoption — build it against real complaint data, not
   speculatively.
4. Pillar 3, once a customer wants agent-proposed fixes auto-applied
   rather than just flagged — the highest-trust, highest-liability
   feature, correctly last.
