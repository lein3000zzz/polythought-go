---
title: The editor
description: Build a workflow, run it and see what each node did.
---

Open **Workflows** and press **New workflow**, or open one you have. The editor has the node
list on the left, the canvas in the middle and the selected node's settings on the right.
**Code**, at the top, shows the whole workflow as TypeScript to edit instead; see
[Workflows as code](/docs/guides/workflow-code/).

## Your workflows

**Workflows** shows each of yours as a card: its name, its description, whether it is
active, how many nodes it has and when it last changed. The search finds them by name and by
description, `/` jumps to it, and **All**, **Active** and **Inactive** narrow the
cards to those switched on or off.

Beside **Active**, a card says how the workflow is doing, such as `+$12.40 paper · ran 5 min
ago`:
- **What it made** in the mode it trades in now, as its **Trading** view says: on paper its
  worth less what it started with, live what its holdings made. Only once it has traded in
  that mode.
- **Its last run,** in red where it failed.

`≈` before the amount means some open positions have no price now, so they count at what
they cost. The cards ask again each minute. Agents read the same through
`get_workflow_stats`.

**Dashboard**, at the top of Workflows, shows them all together, on paper or live, with
**Paper** and **Live**: paper and live are never added together. It has three parts:
- **Totals:** profit, realized and unrealized, open positions, volume, win rate, and the
  runs of the last day and week.
- **Workflows:** a row each, sorted by the column you click, a second click the other way;
  one that trades links to its **Trading** view.
- **Traders:** what copying each trader came to, across every workflow that copies them.

**Customize** shows and hides each part, kept with your account, so another browser shows
the same.

The pin on a card puts it at the top, after the others pinned. Drag a pinned card to
another place among them, by its handle on a touch screen, and let go: **Escape** while
dragging puts it back. Or focus the handle and move the card with the arrow keys. The
order is yours, on every browser. While a search or a filter hides some of them, the
order stays as it is. Pinning changes nothing in the workflow, its version included.

A workflow's **Description** is in its settings with no node selected, saved with it. Using
one of Polythought's library entries, or one of your own, gives the new workflow the
entry's summary as its description; another user's words are not passed on as yours.
Sharing a workflow starts from its description.

The node list and the settings fold to a narrow rail with the button at their top, and
drag wider or narrower by their inner edge: from 200 to 400 pixels for the list, 280 to
640 for the settings. Focused, an edge also moves with the arrow keys, Home and End, and a
double-click puts its panel back to its usual width. The editor keeps each panel's width,
and whether it is folded, for the next time; on a narrow window the settings give way
first, then the list, so the canvas keeps some room.

## Building

- **Add a node**: drag it from the node list onto the canvas. The list is grouped like the
  [node reference](/docs/reference/nodes/).
  Nodes of the [plugins](/docs/self-hosting/plugins/) an admin installed, and you may use, are
  under **Plugins**, each saying which plugin it is from; those that place orders say
  **Trades**.
- **Connect two nodes**: drag from a node's output port, on its right edge, to another
  node's input port, on its left edge.
- **Remove a connection**: select it and press Delete or Backspace. Undo brings it back;
  see [Shortcuts](#shortcuts).
- **Change a node**: select it. Its settings open on the right, with a description of each.
  Changes apply as you type. Settings left empty use their default, shown in grey. With the
  settings folded, double-click the node to open them on it.
- **Fields**: settings that name a field of the items, like If's **Field**, offer the
  fields of the items the node receives as you type, with what each holds: only numbers
  where it compares numbers. A field the items do not have is marked at once, with the one
  you probably meant, like `bestBid` for `bestbid`. After a node that does not say what it
  sends, any field goes.
- **Expressions**: `{{ }}` in a setting is worked out for each item, like
  `{{ dec(spread).times(100) }}`; inside, the editor completes the fields there and
  underlines what does not fit. Switches and dropdowns take one through the **`{{ }}`**
  button by their name. See [Expressions](/docs/guides/expressions/).
- **Tables**: a setting with a row each, like a list of traders, shows how many rows it has;
  **Edit** opens them in a table to add, change, reorder and remove rows. Each column says
  what its cells take, and a cell left empty takes the column's default.
- **Notes** on a node are for you: they show on the canvas and never change what it does.
- **Continue on failure** sends items the node fails on to its `error` port instead of
  failing the execution; see [When items fail](/docs/guides/concepts/#when-items-fail).

## Shortcuts

On the canvas, outside a field, these keys work, Cmd taking the place of Ctrl on a Mac.
**?** lists them all, and so does **Keyboard shortcuts** under the editor's **⋮** menu.

| Keys | What they do |
|---|---|
| Ctrl+Z, Ctrl+Shift+Z or Ctrl+Y | Undo, redo |
| Ctrl+C, Ctrl+X, Ctrl+V | Copy, cut and paste the selected nodes and the connections between them |
| Ctrl+D | Duplicate the selected nodes |
| Delete or Backspace | Delete the selected nodes and connections |
| Ctrl+A, Escape | Select every node, clear the selection |
| Arrow keys, with Shift for further | Move the selected nodes |
| Shift+1, + and - | Fit the workflow in view, zoom in and out |
| Ctrl+S, Ctrl+Enter | Save, run |
| / | Search the node list |

Undo goes back over every change on the canvas: nodes added, deleted or moved, connections,
settings, names and notes, the workflow's name and description, and code applied from the
**Code** view. Typing on in one field is one step, and in a field Ctrl+Z is the field's own.
The **Undo** and **Redo** buttons at the top do the same. Saving starts a new history.

Copied nodes go to your computer's clipboard as text, so they paste into another workflow,
in another tab too, beside the originals or in the middle of the view. Their personal
settings, such as a webhook's URL, a credential or a wallet, stay behind, as sharing to the
library leaves them out: fill them in where you paste. Duplicating keeps everything, as the
copy never leaves the page.

**Settings → Shortcuts** changes the keys of any of them but copy, cut, paste and the arrows:
press **Change**, then the new keys. They are kept for your account, on every browser, and
**Reset** puts one back.

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

**Executions** lists past runs, newest first, with how each ended, in a panel under the
canvas that drags taller by its top edge, up to half the editor. Turn on **Show new runs as
they finish** to watch an active workflow. Select a run to see it on the canvas: each node
shows what it did in that run, and a note says when the run used an older version of the
workflow.

A run an [agent](/docs/architecture/agents/#agents-over-mcp) or an API token is behind says
which: run by it by hand; or started by its trigger while the workflow is on, the agent
having switched it on or changed it while on, until it is switched on or off again by
anyone. Tokens go by their names; **confirmed by you** says the agent's asking waited for
your yes.

## More actions

The **⋮** menu shares the workflow to the [library](/docs/guides/library/) or deletes it.
