# What a plugin declares, and what the browser makes of it

## What happens when a plugin dies

A plugin is a subprocess ([ADR-0039](../adr/0039-one-plugin-transport.md)), and Dusk keeps it up ([ADR-0055](../adr/0055-supervising-plugin-processes.md)).

| Phase | What it means | What Dusk is doing |
| --- | --- | --- |
| `running` | The process is serving its socket | Asking it to observe on its interval |
| `restarting` | It exited and a start is waiting out its backoff | Starting it again, longer each time |
| `failed` | It would not stay up | Nothing. Somebody has to press Start |
| `stopped` | Dusk stopped it: uninstalled, reconfigured, shut down | Nothing, and correctly so |

Backoff doubles from a second, caps at a minute, and gives up after eight attempts.
A process that stays up for a minute resets the count, so a plugin that crashes once a week is restarted for ever while one that crashes on start is not.

**A plugin that is not running answers every call with an error naming how it exited.**
That is not politeness.
An observation is complete by contract, so an empty one deletes everything that plugin had observed ([ADR-0011](../adr/0011-ingester-scheduling.md)), and a restart must never look like a source that went quiet.
A run that fails keeps what the catalog had, and the entity goes visibly stale instead.

An action interrupted by its plugin dying is told so, and a **mutating** one is told its outcome is not known: the process that could have said whether the change landed is the one that went away.
Treat that as "find out", never as "it did not happen".

What the plugin printed before it died survives into the process that replaces it, marked `=` in its output, which is usually where the reason is.

## Where a view mounts

A UI contribution is either **declared**, which is a description Dusk renders with its own React, or **drawn**, which is a custom element the plugin ships and Dusk serves from its own origin ([ADR-0020](../adr/0020-plugin-ui.md)).
Declared is the default, because it runs no JavaScript from the plugin and so asks nobody to decide whether to trust it.

A contribution also names a slot, and the two facts together decide whether it can render at all.
A declared view draws a result set, so it mounts only where something supplies one:

| Slot | What supplies the result set | Declared | Drawn |
| --- | --- | --- | --- |
| `UI_SLOT_ENTITY` | The entity whose page it is on, and `applies_to_kinds` says which pages those are | Yes | Yes |
| `UI_SLOT_ENTITY`, in a page's `view` block | The block's `ref` or `query`, resolved server side ([ADR-0035](../adr/0035-blocks-resolve-server-side.md)) | Yes | Yes |
| `UI_SLOT_PLUGIN` | Nothing. It is the plugin's own page, which is about no entity | **No** | Yes |

**A declared view in the plugin slot is refused**, by `conformance.ValidateDescribe` in the plugin's own tests and again by Dusk when it reads the description, which shows the reason where the view would have been.
It was silently rendering its own `empty` text, which reads as an answer with nothing in it rather than as a view that was never going to work ([ADR-0064](../adr/0064-a-declared-view-mounts-where-a-result-set-comes-from.md)).

The plugin slot is for what has no entity yet, which in practice means creating something, which is interactive: ship an element.
A view over data the plugin already observed belongs on a page, as a `view` block with a query, where the operator asks the question and the plugin decides only how the answer looks.

## What a plugin declares

An action declares its parameters as JSON Schema, on `ActionDescriptor.params_schema`.
One declaration serves every surface: an agent's tool arguments over MCP, a form in the browser, and the schema on `Elicit` when the plugin asks a question mid-action ([ADR-0041](../adr/0041-plugins-reach-agents-as-actions.md), [ADR-0046](../adr/0046-plugins-can-ask.md)).

The rest of this page is the browser half of that: which declared shape becomes which control, and what a form refuses before the plugin ever sees it.
The protocol itself lives in the [SDK](https://github.com/NerdsWhoFish/dusk-plugin-sdk), and a plugin's *configuration* is a typed field list rather than JSON Schema, for the reasons in [ADR-0023](../adr/0023-plugin-configuration.md).

Dusk does not validate a relayed schema, so a shape nothing here can render is a broken form nobody finds until a human sees it.
Declaring one of the shapes below is how that is avoided.

## What each shape renders as

| Declared | Control | Sent as |
| --- | --- | --- |
| `string` | A text box, one row tall, that grows | a string |
| `string` with `format` (below) | A text box, eight rows tall | a string |
| `string` with `enum` | A picker | the chosen string |
| `integer`, `number` | A number box | a number |
| `boolean` | A tick | `true` or `false` |
| `array` of `string` | One item per line | an array of strings |
| `array` of `integer` or `number` | One item per line | an array of numbers |
| `array` of anything else | A JSON box | whatever was written, once it parses as an array |
| `object` with `properties` | Its own small form, one field per property | an object |
| `object` without `properties`, or nested deeper than one level | A JSON box | whatever was written, once it parses as an object |

Every text box is a `textarea`, never an `<input>`, because a JSON Schema string may contain newlines and an input cannot hold one.
A field left empty is **left out of the request** rather than sent as `""`, so a plugin can tell "not given" from "given as nothing".

## Saying a string is prose

A schema says a field wants room with `format`:

```json
{ "type": "string", "format": "markdown", "description": "The record, as MADR text." }
```

| Signal | Meaning |
| --- | --- |
| `"format": "textarea"` | Several lines of plain text |
| `"format": "markdown"` | Several lines of markdown |
| `"contentMediaType": "text/markdown"` | The same as `format: markdown`, for authors who prefer the standard spelling |

`format` is an annotation JSON Schema validators are required to ignore, so declaring one costs nothing and breaks nothing.
It only decides how tall the box starts.
A field without it is still multi-line and still accepts a pasted document; it just opens at one row, which for a whole file is a box somebody has to drag open first.

**Declare it on anything that holds a document.**
A file's `content`, a problem statement, a body of reasoning: all of them are prose, and a one-row box is how a form says "this is a name" when it is not.

## Lists are written a line at a time

An array of strings or numbers is a box you type one item per line into.
Blank lines are spacing and never become empty items, and each line is trimmed.

The consequence is the constraint: **an item cannot contain a newline**, because the newline is the separator.
That is right for the things a list usually holds, and a list of documents is not one of them.
An array whose `items` declare `format` gets the JSON box instead, so the shape stays expressible either way.

If `items` declare an `enum`, a line that is not one of the allowed values is refused with the value named.

## Objects

An object that declares its `properties` is rendered as its own small form, one field per property, each following the table above.
That is what makes a `{ path, content }` file pair reasonable to fill in: a path box and a prose box, rather than a document typed into a JSON string literal with escaped newlines.

Nesting stops there.
A second level of objects, and an open map declared with `additionalProperties`, both get a JSON box, which is pre-filled with a skeleton of the shape the schema describes.
A form that renders arbitrary depth is a schema editor, and a plugin that needs one is better served by asking for less in more steps ([ADR-0046](../adr/0046-plugins-can-ask.md)).

An array of objects gets a JSON box too.
It is the one control that reads as an escape hatch, and deliberately so: that data is almost always produced by something else and pasted, rather than typed.

## What is refused before the plugin sees it

The form will not send:

- a JSON box that does not parse, or parses to the wrong kind of thing
- a line in a number list that is not a number, or not a whole one where `integer` was declared
- a line that is not one of the values `items.enum` allows
- a `required` field nobody filled in, including a `required` property of an object once that object exists at all

Each says so against the field it is about.
Nothing is refused that the schema did not ask for: a constraint Dusk does not read, such as `minimum` or `pattern`, still reaches the plugin to enforce, and a plugin remains the authority on its own input.

## What a form still cannot say

- An item in a list cannot contain a newline. Declare `format` on `items` to fall back to JSON.
- `oneOf`, `anyOf`, and `$ref` are not read. A field declared only that way renders as a plain text box.
- A JSON box is a JSON box. It is honest about the shape and unpleasant to type; prefer a declared `object` where one fits.
