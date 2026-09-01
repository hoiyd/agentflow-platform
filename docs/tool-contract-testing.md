# Tool Contract and Fault Testing

AgentFlow treats every Tool as a contract for a non-deterministic caller. A
Binding is not considered covered merely because its happy-path Handler test
passes: its model-visible schema, runtime validation, failure classification,
result shape, tracing, and durable side-effect boundary must agree.

## Test layers

| Layer | Responsibility | Network or model required |
| --- | --- | --- |
| `Catalog.ValidateCall` | Apply the same compiled schema and canonical identity as the Executor without charging Budget or calling the Handler | No |
| Binding contract suite | Check schema identity, valid/invalid arguments, deterministic result sensor, tracing, and committed side-effect replay | No |
| Executor fault harness | Check invalid arguments, Handler error/panic, timeout, cancel, non-JSON/oversized result, Budget denial, and paired tracing | No |
| Effect gate | Pause deterministically after durable intent, after the external effect, and before settlement; inject journal failures | No |
| Tool selection dataset | Check no-Tool decisions, exact Tool choice, argument validity, recovery action, and required evidence, including adversarial candidates | No |

The Executor and the offline selection evaluator share `Catalog.ValidateCall`.
This prevents an evaluation-only validator from drifting away from production
argument handling. Validation returns canonical arguments, definition revision,
and arguments hash, but cannot execute a Binding or consume Run Budget.

## Binding contract

`internal/testsupport/tooltest.RunBindingContract` is the reusable conformance
runner. Each production Binding supplies:

- one schema-valid argument set;
- invalid argument cases with stable validation codes;
- one representative successful result;
- a deterministic result sensor;
- at least one known-bad result that the sensor must reject.

The default Catalog test is intentionally exhaustive: adding a new default
Binding without a matching contract case fails the suite. Runtime-injected
Bindings, currently `update_task_state`, own an equivalent contract test in
their package.

Result sensors validate useful domain evidence, not only JSON encoding. For
example, the calculator sensor requires a finite numeric value, the time sensor
requires an RFC3339 timestamp, and the Task State sensor requires an applied
mutation with revision identity and durable state.

## Fault and effect fixtures

`RunExecutorFaultHarness` exercises runtime-wide failure semantics with local,
deterministic Bindings. Every failure must remain typed and every trace start
must have one finish. The result-size case verifies a bounded UTF-8-safe preview
rather than treating truncation as an execution failure.

`EffectGateFixture` models the durable protocol:

```text
intent_persisted -> effect_applied -> settlement_pending -> committed
```

Tests inspect persisted state at each boundary without timing sleeps or an
external service. A settlement failure must leave the effect in
`needs_reconciliation`; it must not silently replay an external write.

## Selection dataset

`internal/tools/testdata/tool_selection_golden.json` is a versioned,
deterministic dataset. It covers ordinary no-Tool and correct-Tool decisions,
similar Tool confusion, invalid arguments, execution failure recovery, required
evidence, unsupported tasks, and every current model-visible Binding.

The evaluator is a computational sensor, not an LLM judge. It rejects known
bad candidates with stable finding codes. A future semantic judge may add
advisory diagnostics, but it must not become a required CI gate unless its
reproducibility and false-positive rate are measured.

Policy denial is deliberately absent from the current outcome enum because the
runtime has no Tool Security Policy yet. `TOOL-003` will add the production
decision and its deterministic dataset case through this harness; the test
suite does not invent behavior that production cannot emit.

## Adding a Tool

1. Register one Descriptor/Binding contract and retain owner tests for real
   domain behavior.
2. Add a `BindingContract` with valid arguments, failure cases, a result sensor,
   and known-bad outputs.
3. Add a realistic selection task and a no-Tool or similar-Tool negative case
   to the golden dataset.
4. For external effects, test idempotent replay and the uncertain settlement
   path with the effect gate or an equivalent deterministic fixture.
5. Run the focused suite and race detector:

```bash
cd apps/api
go test ./internal/tools ./internal/taskstate
go test -race ./internal/tools ./internal/taskstate
```

The shared harness supplements owner tests; it does not replace assertions for
the Tool's real data source, permissions, or domain semantics.
