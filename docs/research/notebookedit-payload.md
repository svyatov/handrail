# Does `NotebookEdit` carry `new_source`, or do the docs?

Date: 2026-08-24
Purpose: primary-source research for handrail issue #108 (wayfinder ticket T23). `internal/harness/harness.go:110-111` reads `notebook_path` and `new_source` out of a Claude Code `NotebookEdit` tool call. Claude Code's public tools reference names only `cell_id`, `cell_type` and `edit_mode`. If handrail is reading keys that are not on the wire, every `content` and every `path` condition silently never fires on a notebook edit. This paper settles which side is right.

**Answer: handrail is right.** A `NotebookEdit` `PreToolUse` payload carries `tool_input.notebook_path` and `tool_input.new_source` verbatim, and both are required, so neither can be absent from a payload that reaches a hook. The published tools reference is incomplete, not contradictory: it describes the tool's behaviour in prose and never enumerates its full input schema.

Evidence class: **shipped implementation**, read out of the Claude Code binary installed on the research machine, corroborated by the Agent SDK TypeScript reference. No observed live payload was obtained; section 5 records why.

## 0. Sources and how to reproduce them

The strongest source used here is the shipped implementation, not a doc page.

- **Binary**: `~/.local/share/claude/versions/2.1.241`, the target of the `claude` symlink at `~/.local/bin/claude` on a native install. `claude --version` reports `2.1.241 (Claude Code)`. It is a Mach-O arm64 single-file build with the JavaScript bundle embedded uncompressed and greppable. Byte offsets below are into that file and are valid for that build only; the surrounding source text is quoted so the claims survive a version bump.
- **Docs**, fetched 2026-08-24 as raw markdown from the `.md` endpoints listed at `https://code.claude.com/docs/llms.txt`:
  - Tools reference: https://code.claude.com/docs/en/tools.md
  - Hooks reference: https://code.claude.com/docs/en/hooks.md
  - Agent SDK TypeScript reference: https://code.claude.com/docs/en/agent-sdk/typescript.md

## 1. The tool's input schema, from the shipped code

The `NotebookEdit` tool object is built at byte offset 293968039, opening `e2e=ss({name:ok,ruleContentField:"notebook_path",...})`. Its `inputSchema` getter returns the schema factory `fqv`, defined verbatim at byte offset 293966527:

```js
fqv=Se(()=>pi({
  notebook_path:H().describe("The absolute path to the Jupyter notebook file to edit (must be absolute, not relative)"),
  cell_id:H().optional().describe("The ID of the cell to edit. When inserting a new cell, the new cell will be inserted after the cell with this ID, or at the beginning if not specified."),
  new_source:H().describe("The new source for the cell"),
  cell_type:Dr(["code","markdown"]).optional().describe("The type of the cell (code or markdown). If not specified, it defaults to the current cell type. If using edit_mode=insert, this is required."),
  edit_mode:Dr(["replace","insert","delete"]).optional().describe("The type of edit to make (replace, insert, delete). Defaults to replace.")
}))
```

So the exact key list is:

| Key | Required | Type |
| :--- | :--- | :--- |
| `notebook_path` | **yes** | string, absolute |
| `new_source` | **yes** | string |
| `cell_id` | no | string |
| `cell_type` | no | `"code"` or `"markdown"` |
| `edit_mode` | no | `"replace"`, `"insert"` or `"delete"`, defaults to `replace` |

Two details matter for handrail.

**`new_source` is required in every edit mode, including `delete`.** It carries no `.optional()`. The tool's own classifier projection at byte offset 293968555 confirms the intent rather than an oversight: `...t==="delete"?{ignored_source:e.new_source}:{adds:e.new_source}`. On a delete the value is present and ignored, not absent.

**The schema is strict, so no other keys can appear.** `pi` is the bundled zod `strictObject` helper: at byte offset 284782588 the bundle defines `function pi(e,t)` as a plain object schema plus `catchall:Wxn()`, immediately after `function ye(e,t)` which has no catchall and immediately before the loose variant `function Ec(e,t)` whose catchall is `Dn()`, the same `unknown()` used for `tool_input` itself. The hook-rewrite path corroborates it by filtering out exactly the strict-object error code: `f.error.issues.filter((h)=>h.code!=="unrecognized_keys")` at byte offset 297000142.

The tool's name is the literal string: `var ok="NotebookEdit"` at byte offset 287646746.

## 2. The hook payload path, from the shipped code

A published input schema is not an observed payload, so the question is whether anything transforms the input between the model and the hook's stdin. The path is four hops and none of them renames a key.

**Hop 1, parse.** In the tool executor at byte offset 298425398:

```js
let g=r,y=null;
if(e.coerceInput){if(y=e.coerceInput(r),y!==null)g=y.input}
let _=e.inputSchema.safeParse(g);
```

`NotebookEdit` declares no `coerceInput`, so `g` is the model's raw input and `_` is the strict-schema parse of it. A parse failure returns an `InputValidationError` tool result before any hook runs, which is why a payload that reaches a hook always has both required keys.

**Hop 2, backfill.** At byte offset 298428593:

```js
let v=[],T=_.data;
...
let C=T,k=e.backfillObservableInput&&typeof T==="object"&&T!==null?{...T}:null;
if(k)e.backfillObservableInput(k),T=k;
```

`NotebookEdit`'s implementation of that hook, at byte offset 293968235, is:

```js
backfillObservableInput(e){if(typeof e.notebook_path==="string")e.notebook_path=rs(e.notebook_path)}
```

It normalises the path in place. It adds no key and renames none. This is the notebook-tool twin of the guarantee the hooks reference states for `Write`, `Edit` and `Read`: "Claude Code expands `~` and relative paths before hooks run, so a hook that matches on paths can't be bypassed via `~` or a relative spelling of the same path" (https://code.claude.com/docs/en/hooks.md#pretooluse-input). The reference does not extend that sentence to `NotebookEdit`; the shipped code does, through the identical `backfillObservableInput(e){...e.file_path=rs(e.file_path)}` seen on `Edit` at 293869706, on `Write` at 293878696 and on `Read` at 298769222.

**Hop 3, dispatch.** The same executor line continues `for await(let J of qoo(n,e,T,t,i.message.id,a,l,c))`, and `qoo` at byte offset 296999464 opens:

```js
async function*qoo(e,t,r,n,o,i,s,a){...for await(let p of Kmr(t.name,n,r,e,yn(e).mode,e.abortController.signal))...}
```

`t.name` is the tool object's canonical name, not whatever alias the model may have used to call it. `toolAliases` is a user- and SDK-supplied record (`toolAliases:so(H(),...)`), consulted only when resolving the tool object, so an alias cannot change the `tool_name` a hook sees.

**Hop 4, serialise.** `Kmr` is `executePreToolHooks`, at byte offset 294038635:

```js
async function*Kmr(e,t,r,n,o,i,s=eb,a){
  ...
  let c={...v_(n.session,tr(),o,n),hook_event_name:"PreToolUse",tool_name:e,tool_input:r,tool_use_id:t};
  yield*gB({session:n.session,hookInput:c,...});
}
```

`tool_input` is the object from hop 2, spread in unchanged. The declared payload shape agrees: at byte offset 287312474 the bundle carries the zod schema `Pqb=Se(()=>YP().and(ye({hook_event_name:Ct("PreToolUse"),tool_name:H(),tool_input:Dn(),tool_use_id:H()})))`, matching `PreToolUseHookInput` in the SDK reference (https://code.claude.com/docs/en/agent-sdk/typescript.md, `tool_input: unknown`).

**Conclusion.** `tool_input` for a `NotebookEdit` `PreToolUse` hook is the strict-parsed tool input with `notebook_path` resolved to an absolute path. Its keys are exactly `notebook_path`, `new_source`, and whichever of `cell_id`, `cell_type` and `edit_mode` the model supplied.

## 3. What the published docs say, and do not say

The tools reference does describe `NotebookEdit`, in prose, in full (https://code.claude.com/docs/en/tools.md#notebookedit-tool-behavior):

> NotebookEdit modifies a Jupyter notebook one cell at a time, targeting cells by their `cell_id`. It doesn't perform string replacement across the notebook the way Edit does on plain files.
>
> Three edit modes control what happens to the target cell:
>
> * `replace`: overwrite the cell's source. This is the default.
> * `insert`: add a new cell after the target. With no `cell_id`, the new cell goes at the start of the notebook. Requires `cell_type` set to `code` or `markdown`.
> * `delete`: remove the target cell.

That is the whole of it. Of the five schema keys it writes only two by name, `cell_id` and `cell_type`; `edit_mode` appears only as its three values, and the strings `new_source` and `notebook_path` appear nowhere on the page. Nor on the hooks reference. The hooks reference's per-tool breakdown of `tool_input` under "PreToolUse input" (https://code.claude.com/docs/en/hooks.md#pretooluse-input) opens with "The `tool_input` fields depend on the tool:" and then documents exactly twelve tools: `Bash`, `PowerShell`, `Write`, `Edit`, `Read`, `Glob`, `Grep`, `WebFetch`, `WebSearch`, `Agent`, `AskUserQuestion`, `ExitPlanMode`. `NotebookEdit` is not among them.

So the docs never contradict handrail. They are silent on the two keys handrail reads, and the prose section that mentions the tool was written to explain edit modes rather than to enumerate a schema. The only doc claim that could have been read as a contradiction, that the tool's inputs are `cell_id`, `cell_type` and `edit_mode`, is an artefact of reading a behaviour section as a parameter list.

One thing the hooks reference does state generally, and which now applies: "The `tool_name`, `tool_input`, and `tool_use_id` fields are event-specific" (https://code.claude.com/docs/en/hooks.md#hook-input-and-output). It never says `tool_input` is a transformed shape, and section 2 shows it is not.

## 4. The Agent SDK reference corroborates the binary

The TypeScript reference publishes (https://code.claude.com/docs/en/agent-sdk/typescript.md#notebookedit):

```typescript
type NotebookEditInput = {
  notebook_path: string;
  cell_id?: string;
  new_source: string;
  cell_type?: "code" | "markdown";
  edit_mode?: "replace" | "insert" | "delete";
};
```

That is `fqv` from section 1, key for key, optionality for optionality, in the same declaration order.

The corroboration is stronger than a second opinion, because the same page also publishes `NotebookEditOutput` with ten fields in the order `new_source`, `old_source?`, `cell_id?`, `cell_type`, `language`, `edit_mode`, `error?`, `notebook_path`, `original_file`, `updated_file`. The binary's output schema `mqv` at byte offset 293967244 declares those ten fields in that same order. A published type matching a shipped schema field for field, in declaration order, on both the input and the output side, is a generated artefact rather than a hand-written doc. So the SDK reference and the binary are not two independent witnesses; they are the same schema published twice. What that buys is a durable citation: the SDK reference can be cited in place of a byte offset that dies at the next release, and section 2 is what closes the remaining gap between a published schema and the wire.

## 5. No live payload was captured, and why

Three attempts were made to observe a real payload, all on this machine, all outside the repository. None produced one, and the reason is environmental rather than a fact about the payload.

- **Existing transcripts.** Every `~/.claude/projects/**/*.jsonl` was scanned for `tool_use` content blocks with `"name": "NotebookEdit"`. Zero blocks exist. The tool name appears in those files only inside tool-list text.
- **Attempt 1, headless run.** A throwaway notebook in `/tmp` plus a settings file with a `PreToolUse` hook matching `NotebookEdit` whose command dumps stdin to a file and exits 2. `claude -p --settings ... --permission-mode bypassPermissions` reported that `NotebookEdit` was not in its tool list and not among the deferred tools.
- **Attempt 2, same run with an explicit `ToolSearch select:NotebookEdit` first.** `ToolSearch` returned "No matching deferred tools found".
- **Attempt 3, isolated config.** Re-running with `CLAUDE_CONFIG_DIR` pointed at a temporary directory dropped the user settings but also the credentials: the run exited with "Not logged in".

The cause of the first two is local configuration: the research machine's user settings list `NotebookEdit` under `permissions.deny`, and a denied built-in is removed from the session's tool list entirely, so no call and therefore no hook can occur. Capturing a live payload therefore needs either a machine without that deny rule or a temporary edit to the user's own settings, which is out of scope for a research ticket. The shipped-code chain in section 2 is a strictly stronger source than a single observed payload anyway: it shows not just what one payload contained but that no code path between the model and the hook can produce anything else.

One incidental fact from those attempts is worth recording because it is easy to misread: the tool object sets `shouldDefer:!0` (byte offset 293968161), so `NotebookEdit` is a deferred tool that a session loads through `ToolSearch` rather than one always present in the tool list. The attempts above cannot confirm the deferral behaviour empirically, since the deny rule masks it.

## 6. What this means for handrail

**`notebook_path` and `new_source` are the right keys, and the lookup order in `harness.go` is already correct.**

`internal/harness/harness.go:110-111`:

```go
set(&p, "path", in.ToolInput, "file_path", "notebook_path")
set(&p, "content", in.ToolInput, "content", "new_string", "new_source")
```

A `NotebookEdit` payload has no `file_path`, no `content` and no `new_string`, so both lookups fall through to the notebook key and hit. `classify` at `internal/harness/harness.go:154` already maps `NotebookEdit` to `file_edit`. Nothing in the adapter needs to change.

Two smaller confirmations for the `path` field. First, `notebook_path` is absolute by the time a hook sees it, per hop 2, so the same normalisation assumptions handrail makes for `file_path` hold. Second, `new_source` is required in every mode, so a `content` condition on a `delete` still evaluates against the source the model proposed rather than against an empty string. Whether that is the desired semantics for a `delete` is a spec question, not an adapter one, and this paper does not answer it.

### The capability matrix row

`docs/spec.md:108`, the "Tool names on the wire" row, currently reads `Bash`, `PowerShell`, `Edit`/`Write`, `Read`, `mcp__<server>__<tool>` for Claude Code. It omits `NotebookEdit`, which `classify` handles and which this paper establishes is a live, distinctly named tool in 2.1.241. The row understates the surface in one way that matters: every other `file_edit` tool it names carries its path in `tool_input.file_path` and its content in `tool_input.content` or `tool_input.new_string`, and `NotebookEdit` carries neither. A reader of that row alone would conclude handrail's `path` and `content` fields are uniform across Claude Code's file tools, which is exactly the assumption that would have made this ticket a real bug.

Suggested amendment, for the maintainer to take or leave: add `NotebookEdit` to the row with the note that it carries `tool_input.notebook_path` and `tool_input.new_source` rather than `file_path` and `content`, in the same shape as the row's existing `PowerShell` and `apply_patch` notes. No other row is affected.

### One adjacent finding, outside the ticket

Claude Code's own permission-rule validator warns against the `NotebookEdit(path)` form. At byte offset 286024155 the settings validator emits, quoted here exactly as the bundle spells it:

```js
let a=o.toolName==="Write"||o.toolName==="NotebookEdit"||o.toolName==="MultiEdit"?"Edit":o.toolName==="Glob"?"Read":void 0;
if(a!==void 0&&!o.ruleContent.includes(":*"))return{valid:!0,warning:`${Ld(o)} is not matched by file permission checks \u2014 only ${a}(path) rules are. Use ${Ld({toolName:a,ruleContent:o.ruleContent})} instead (${a} rules cover all file-${a==="Edit"?"editing":"reading"} tools).`}
```

The tools reference says the same in prose: "Permission rules use the `Edit(...)` path format. A rule like `Edit(notebooks/**)` covers NotebookEdit calls on files in that directory" (https://code.claude.com/docs/en/tools.md#notebookedit-tool-behavior).

This does not affect the hook path, where `NotebookEdit` is matched by tool name and `internal/rule/hookify.go:390-400` correctly classifies it. It only constrains the *native permission entry* a rule should recommend alongside a hook: for a path-scoped notebook guardrail that entry must be `Edit(<path>)`, never `NotebookEdit(<path>)`. Whether handrail ever emits the latter was not audited here.

## 7. What could not be established from a primary source

- **An observed `NotebookEdit` `PreToolUse` payload.** None exists in local transcripts and three capture attempts failed for the configuration reasons in section 5. Every claim about the wire shape in this paper is derived from the shipped implementation, not from a payload seen on stdin.
- **Whether the tools reference's omission of `new_source` and `notebook_path` is deliberate.** The page documents behaviour, not schemas, for several tools. Nothing states whether the per-tool `tool_input` tables in the hooks reference are meant to be exhaustive; twelve tools are covered and the rest are not, with no note saying why.
- **Whether `NotebookEdit`'s deferral is unconditional.** The tool object sets `shouldDefer:!0`, but the local deny rule prevented observing whether a session offers it through `ToolSearch` on demand, and no doc page states the deferral rule for built-ins.
- **Whether a `PreToolUse` hook's `updatedInput` for `NotebookEdit` is re-checked against deny and ask rules.** Same open question as `docs/research/event-model-v2.md` records for `PreToolUse` generally; nothing tool-specific was found.
- **The behaviour on any Claude Code build other than 2.1.241 on macOS arm64.** The byte offsets are build-specific and only this build was read. The Agent SDK reference agreeing with it is evidence the schema is stable, not proof.
- **Whether handrail's `sync` or its rule skills ever emit a `NotebookEdit(path)` permission entry.** The adjacent finding in section 6 identifies the constraint; auditing handrail against it was outside this ticket.
