# claude-harness

Version-controlled configuration for Claude Code.

## Contents

- `CLAUDE.md` — global instructions loaded into every Claude Code session:
  - **Plan and delegate by cost** — plan tasks first and route subtasks to the cheapest suitable model (Haiku / Sonnet / Opus).
  - **Feature branches and PRs** — one branch per feature, PR into `main` with full notes when ready.

## Install

Symlink the repo's `CLAUDE.md` into place so edits here apply globally:

```sh
ln -sf "$(pwd)/CLAUDE.md" ~/.claude/CLAUDE.md
```
