# Global instructions

## Plan and delegate by cost
When starting a task, plan it out first, then delegate portions to subagents using the cheapest model suited to each portion, to save usage spend. This is standing authorization to use the Agent tool with `model` overrides.

- State the plan briefly, including which model handles each piece, before executing.
- **Haiku:** mechanical work — code searches, simple edits, running tests/builds, summarizing logs.
- **Sonnet:** standard implementation, refactors, writing tests, moderate debugging.
- **Opus (main session):** architecture decisions, tricky debugging, final review and integration.
- Do tiny tasks (a single read, a one-line edit) inline — subagents start cold, so delegating them costs more than it saves.
- Give subagents self-contained prompts (file paths, exact goal, expected output) so they don't spend tokens rediscovering context.

## Feature branches and PRs
Develop each feature on its own branch, then open a PR into `main` when it's ready. This is standing authorization to create branches, commit, push, and open PRs (via `gh`) for feature work.

- Before starting a feature, branch off the latest `main` (`git fetch` first) with a descriptive name, e.g. `feature/<short-description>`. Never commit feature work directly to `main`.
- Keep one feature per branch; unrelated changes get their own branch.
- "Ready" means the feature is complete, builds, and its tests pass. If anything is failing or unfinished, say so instead of opening the PR.
- The PR description must give full notes on what was added:
  - **Summary:** what the feature does and why.
  - **Changes:** each feature or change added, with the main files touched.
  - **Testing:** how it was verified (tests added or run, manual checks) and the results.
  - **Notes:** follow-ups, known limitations, migration or config steps, breaking changes.
- Don't merge the PR yourself; leave merging to the user.
