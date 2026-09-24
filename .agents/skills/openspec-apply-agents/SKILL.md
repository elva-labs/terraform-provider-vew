---
name: openspec-apply-agents
description: Implement an OpenSpec change with Codex subagents assigned to independent task groups. Use when the user asks to delegate or parallelize OpenSpec apply tasks.
---

# Apply an OpenSpec change with subagents

Use the adjacent `openspec-apply-change` skill for change selection, store handling, status and instruction commands, context files, blocked states, and completion criteria. This skill changes how ready tasks are carried out.

## Before dispatch

1. Read the change status and `openspec instructions apply --json`, with the selected store flag when applicable. Read every returned context file and inspect existing work in the target repo.
2. Establish the edit boundary from `actionContext` and the selected root:
   - For an ordinary repo-local change, treat `actionContext.allowedEditRoots` as the OpenSpec edit boundary. Each proposed implementation path must also be inside the session's writable scope. A proposal naming another repository does not add it to the allowed roots.
   - For a registered standalone store shared by sibling repositories, OpenSpec 1.13.x reports only the store root in `allowedEditRoots`. In this case, treat the registered store root as authorized for planning artifacts and task tracking, and treat the current workspace or Git root as authorized for implementation when the change proposal, tasks, or other planning artifacts explicitly identify that repository as affected. Do not require the implementation root to appear in `allowedEditRoots` for this store-backed case.
   - Do not infer permission for other sibling repositories merely because the store's general context lists them. They must be explicitly affected by the selected change and available in the session's writable scope. If the registered store is outside the default writable scope, use the available approval or escalation mechanism when a planning or tracker edit is needed instead of blocking implementation solely for that reason.
   - In every other scope mismatch, stop and report the exact gap before spawning agents or editing code.
3. Build task groups from actual dependencies and likely file ownership. Dispatch only groups that can proceed without waiting on each other. Keep shared files or tightly coupled tasks with one owner or run them in sequence.

## Size and route task groups

Match each assignment to the reasoning it needs. Group related small checklist items into one bounded assignment when they share context and ownership. Split a broad task into clear implementation pieces for delegation while keeping its OpenSpec checkbox incomplete until the whole task is done. Do not bundle a simple edit with an unresolved design decision merely to give one worker more work.

- Use `gpt-6-luna` with low or medium reasoning for clear, narrow work: documentation, examples, mechanical field mappings, and small edits that follow an established pattern.
- Use `gpt-6-sol` with medium reasoning for ordinary coding that needs judgment across a few files, such as a resource implementation with defined behavior.
- Keep ambiguous API semantics, cross-resource design decisions, and difficult integration or debugging with the coordinator; delegate a bounded investigation to `gpt-6-astra` only when its deeper reasoning is justified.

These are starting points, subject to the models actually available to `spawn_agent`. If a smaller worker finds uncertainty or a mismatch with the spec, have it stop and report the issue. Reassess the task before assigning it to a stronger model; do not repeatedly retry the same vague assignment. When overriding a subagent's model or reasoning effort, use a limited `fork_turns` value and include the needed context in its task message, since a full-history fork cannot take a model override.

## Delegate

- Spawn subagents for independent groups when the session's agent tools and available slots permit. Give each worker the change name, store, assigned task IDs, relevant context, permitted files or area, and any verification required by the task. Tell workers to preserve existing changes and report blockers immediately.
- Workers edit their assigned implementation files and report completed task IDs, changed files, verification performed, and any unresolved issue. Workers do not edit the OpenSpec task tracker or mark checkboxes.
- The coordinator owns shared files, integration decisions, and the task tracker. Review each worker's result against its task, resolve integration issues, then mark its checkbox complete promptly. Never mark a task complete from a worker's claim alone.
- If no tasks can safely run at the same time, use one worker for a suitable group or continue sequentially. Do not force parallel edits to overlapping files.

## Finish or pause

Re-read apply instructions after integration to confirm progress and state. Perform verification required by the change or user. Report completed tasks and remaining work. On an unclear requirement, design gap, blocked OpenSpec state, or edit-scope mismatch, stop dependent work and explain the gap; preserve all existing work.
