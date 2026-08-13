# What a plugin declares, and what the browser makes of it

An action declares its parameters as JSON Schema, on `ActionDescriptor.params_schema`.
One declaration serves every surface: an agent's tool arguments over MCP, a form in the browser, and the schema on `Elicit` when the plugin asks a question mid-action ([ADR-0041](../adr/0041-plugins-reach-agents-as-actions.md), [ADR-0046](../adr/0046-plugins-can-ask.md)).

This page is the browser half of that: which declared shape becomes which control, and what a form refuses before the plugin ever sees it.
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
