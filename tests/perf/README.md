# Scale and performance verification

Dashboard's scale, graph-ceiling and mixed-load acceptance scenarios already live
with the authorized Lab test harness. See [scale](../../docs/LAB_SCALE.md),
[mixed load](../../docs/LAB_MIXED_LOAD.md) and
[graph client acceptance](../../docs/GRAPH_CLIENT_ACCEPTANCE.md).

These scenarios are explicit opt-in tests with synthetic data and bounded resource
budgets. They are not a lightweight benchmark and must not run automatically
against a developer's existing Lab. Record source versions, configuration and
receipt provenance when collecting fresh measurements.
