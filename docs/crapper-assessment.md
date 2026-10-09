# Reading the CRAP report

This is how to read `.metrics/crap-report.txt` and `.metrics/inventory.csv` and decide what is
worth doing. It describes a method, not a result: the scores in any given run depend on the
code and the suites at that moment, so regenerate the report rather than trusting a number
quoted from elsewhere.

For how the report is produced, what runs it, and the limits of the number, see
[coverage.md](coverage.md).

## Start with the inventory, not the score

The report ranks worst first, but the score combines complexity with an uncovered-path
penalty. Read `.metrics/inventory.csv` first and sort by `complexity` to find the
functions carrying the most decisions, because those are the ones a reader has the most
trouble reasoning about regardless of coverage. Then look at `coverage_recorded`.

That column is the single most important field in the inventory, because the score does not
preserve the distinction:

| `coverage_recorded` | Meaning | What it proves |
| --- | --- | --- |
| `no` | No matching coverage record | No measurement for this function in these reports. |
| `yes`, coverage 0% | A record matched, but no covered statements or branches were found in the span | No recorded coverage; check instrumentation and the span before judging tests. |
| `yes`, coverage high | Many instrumented statements or branches were entered | Execution coverage, not proof that outcomes were asserted. |

crapper scores a missing record as 0%, so a function nobody measured and a function measured
as untested produce the same number. Before treating any score as a finding, check whether
there is a measurement behind it. A function with no record needs a measurement first, not a
refactor.

Two measurement limits produce a low number with no code problem behind it. Go profiles
instrument each package for its own tests by default, so calls made from another package's
tests stay unrecorded; cross-package `-coverpkg=./...` instrumentation is not used here. And a
source outside every test suite's scope, the static site among them, has no record at all.
Neither means the behaviour is missing.

## Separate the decisions from the guards

Sort by complexity, then classify what the complexity is spent on.

**Guards are not defects.** A function that opens a transaction, takes a lease, checks
idempotency, enforces a signing algorithm or issuer, or writes an outbox row in the same unit
of work will have high cyclomatic complexity because failure handling is real branching. The
missing coverage often sits exactly on the failure paths: the rollback, the stale lease, the
duplicate request, the rejected token, the commit that fails after the mutation. Removing that
complexity to lower a score removes the protection. Write the test instead.

**Decision logic is the usual target.** Rendering, formatting, validation, and dispatch code
whose branches are a catalogue of optional cases is where extraction genuinely helps: each
branch becomes a named function you can test on its own. Judge it by whether the extracted
pieces get their own tests, not by the score moving. Complexity that moves into a new
function is not complexity removed.

**Poor targets.** Generated protobuf and MockGen methods, `main` and route-registration
startup wiring, configuration accessors, declaration-heavy UI primitives, and static page
rendering can score poorly without revealing a useful behaviour test. Investigate actual
decisions and risk before treating a score reduction there as progress.

## Prioritise

Rank candidates by behaviour risk, not by score:

1. **Domain operations with partial coverage.** A repository or service method with real
   coverage and uncovered outcomes — conflict, rollback, stale lease or generation,
   cancellation, commit failure, partial write. These are the findings worth acting on.
   Read which outcomes are missing before proposing anything, and keep transaction ownership
   and mutation/outbox atomicity visible in the result. Do not introduce a generic transaction
   framework to make one method look tidier.
2. **UI decisions with no measurement.** A control that filters, selects, or mutates state is
   a real behaviour with a real user-visible contract. Test it the next time you touch it.
3. **Genuine duplication.** The same branching repeated across modules. Deduplicating is worth
   doing on its own merits, independent of the score.
4. **Everything else.** Leave it. A high score alone is not a defect.

Two things worth resisting. First, a project-wide complexity refactor: it churns working code
to move a number that is only a ranking aid. Second, the rank the report gives you when the
underlying candidates are all guards or generated code, which is common enough that the top of
the list is frequently the wrong place to start.

## Add behaviour tests, not coverage numbers

A test earns its place by pinning a decision the code could otherwise make the other way.
Prefer cases that assert an outcome — a rejection, a conflict, a preserved invariant — over
tests that assert a rendering happened. Adding assertions to cover an untested branch without
knowing what that branch is for converts an open question into a false guarantee.

Changing the measured source set can change the numbers without any behaviour changing: a
new source file that no test imports enters the denominator at 0%. Treat a percentage that falls because the measured
set grew as scope, not regression. Compare like with like before drawing any trend.

For the static site, validation of MDX pages, OpenAPI operations, and generated documents is
the evidence that exists. Behaviour tests for OpenAPI dereferencing including cycles and
unresolved references, search indexing, and document headings are worth adding when those
utilities change. Do not duplicate static markup with trivial tests to move the denominator.

## Claims the report cannot support

The score measures complexity and coverage only. It says nothing about runtime performance,
latency, memory, query plans, infrastructure, or security configuration, and no observation in
these reports establishes any of them.

A hypothesis about performance, such as per-event copying and sorting in a live log path or
unbounded retained state, is a candidate for investigation, not a finding. Establish the
problem with measurements of long-run CPU, heap growth, retained item count, and render time
before changing anything. Memoization does not bound retained state and virtualized rendering
does not bound the data buffer, so neither is a fix on its own. The same applies to database
indexing, batching, and runtime concurrency tuning: no evidence here supports any of them.

Keep that discipline in both directions. Do not claim a performance improvement because a
score fell, and do not treat an absent measurement as an absent problem.
