# Profile experiments

[Overview](../README.md) · [Setup](setup.md) · [Tools](tools.md) · [Operations](operations.md) · [Experiments](experiments.md) · [Development](development.md)

## Experiments

```sh
workerctl experiment start glm-vs-deepseek --profiles glm,deepseek
workerctl experiment status
workerctl run "First task"     # glm
workerctl run "Second task"    # deepseek
workerctl experiment report glm-vs-deepseek
workerctl experiment stop
```

One active experiment per shared repository identity; linked worktrees participate together. Allocation advances transactionally only when a run is created, so busy requests do not consume assignments. Continuations keep their original assignment. Experiment names are unique per repository; use a new name for a new trial.

Reports group tasks by profile and give values **with sample counts** (`n`), using null for unavailable denominators:

- First-pass acceptance among runs with a current external verdict.
- Mean correction iterations to acceptance (`iteration - 1`) among accepted runs.
- Execution failure rate among finished runs; failed/cancelled/interrupted executions count as failures.
- Test pass rate among current externally reviewed test outcomes.
- Mean execution duration where all iteration finish times are known (excludes time awaiting review; interrupted runs have unknown end times).
- Mean cost among finished runs with complete reported costs.
- Total finished worker cost divided by accepted tasks, only if all finished tasks have known cost.

Tiny sample counts are descriptive, not evidence that one model is better. Active runs are excluded from outcome/cost denominators. Changing a profile's model during an experiment mixes treatments; use stable profile names/configuration for a trial. Hidden/E2E and intervention outcomes remain available in recorded reviews, but V1 reports do not aggregate them.
