---
title: The editor
description: Build a workflow, run it and see what each node did.
---

Open **Workflows** and press **New workflow**, or open one you have. The editor has the node
list on the left, the canvas in the middle and the selected node's settings on the right.

## Building

- **Add a node**: drag it from the node list onto the canvas. The list is grouped like the
  [node reference](/docs/reference/nodes/); hide it with the button at its top for more room.
- **Connect two nodes**: drag from a node's output port, on its right edge, to another
  node's input port, on its left edge.
- **Remove a connection**: select it and press Delete or Backspace.
- **Change a node**: select it. Its settings open on the right, with a description of each.
  Changes apply as you type. Settings left empty use their default, shown in grey.
- **Fields**: settings that name a field of the items, like If's **Field**, offer the
  fields of the items the node receives as you type, with what each holds: only numbers
  where it compares numbers. A field the items do not have is marked at once, with the one
  you probably meant, like `bestBid` for `bestbid`. After a node that does not say what it
  sends, any field goes.
- **Expressions**: `{{ }}` in a setting is worked out for each item, like `{{ spread * 100 }}`;
  switches and dropdowns take one through the **`{{ }}`** button by their name. See
  [Expressions](/docs/guides/expressions/).
- **Notes** on a node are for you: they show on the canvas and never change what it does.
- **Continue on failure** sends items the node fails on to its `error` port instead of
  failing the execution; see [When items fail](/docs/guides/concepts/#when-items-fail).

## Saving

Press **Save**, or Ctrl+S (⌘S on a Mac). Each save makes a new version, shown at the top.
The server checks the whole workflow when you save: a setting that is missing or wrong, a
field the items do not have, or an expression that does not compile, is marked in red on
its node, and nothing is saved until
it is fixed. Workflows saved before keep running as they are; the field check applies the
next time you save them.

If the workflow changed elsewhere since you opened it, say in another tab, the save is
refused rather than overwriting the other change. Reload to get the latest version.

## Running

**Run** starts the workflow now, from its Manual trigger, or from its Schedule if it has no
Manual one. With unsaved changes the button reads **Save and run**.

When the run finishes, each node shows how it went and how many items it sent. Select a
node and open its **Output** tab to see the items it sent on each port: runs you start by
hand keep the first few.

## Active workflows

Switch **Active** on to let the workflow's Schedule and Market activity triggers start it on
their own. Save your changes first: an active workflow always runs its saved version. Switch
it off to stop it.

## Executions

**Executions** lists past runs, newest first, with how each ended. Turn on **Show new runs as
they finish** to watch an active workflow. Select a run to see it on the canvas: each node
shows what it did in that run, and a note says when the run used an older version of the
workflow.

## More actions

The **⋮** menu shares the workflow to the [library](/docs/guides/library/) or deletes it.
