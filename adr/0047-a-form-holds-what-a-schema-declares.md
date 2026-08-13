# 47. A form holds what the schema declares, not what the type is called

Date: 2026-08-13

## Status

Accepted

## Context and Problem Statement

A plugin declares its action parameters as a JSON Schema and Dusk renders a form from it.
The renderer handled three cases: a checkbox for `boolean`, a `<select>` for anything with `enum`, and a single-line `<input>` for everything else, storing whatever was typed as a string.

That is not a rendering shortcoming, it is a form that cannot hold the types it accepts.
A parameter declared `type: array` had no control at all: whatever was typed went to the plugin as a string and was refused.

It was found the only way things like this are found.
A plugin shipped whose every action takes either an array of strings, an array of objects, or a whole markdown document, and none of it could be filled in.
The first real attempt in the browser came back "there is nothing to check: records is needed, and not something to invent", which is the plugin correctly refusing a form that could not express the request.

There is a second half. [ADR-0046](0046-plugins-can-ask.md) made a plugin able to ask a question mid-action, and the browser renders that question from a schema too, through the same code.
So the same defect silently applied to elicitation, which is the surface where a human is supposed to be answering.

## Considered Options

1. **Leave it, and have plugins declare only scalars.** Anything structured becomes a string the plugin parses.
2. **A `maxLength` threshold decides what is long.** Above some number, render a textarea.
3. **A description heuristic.** Guess from the words in the field's description.
4. **A `format` annotation the author sets deliberately**, with the control derived from the declared shape.
5. **A Dusk-specific extension keyword**, such as `x-dusk-control: textarea`.

## Decision Outcome

Chosen: **option 4**, with the control derived from the schema's shape and `format` deciding only how a string field opens.

A string is a textarea. An array of scalars is one item per line. An array of anything else, and any object nested deeper than one level, is a JSON box with the skeleton in its placeholder. A declared object gets a real sub-form.

### Every string is a textarea, and that is the whole argument in miniature

A JSON Schema string may contain newlines.
An `<input>` cannot hold one.
So the old control could not express the type it claimed to accept, which is the same defect as the missing array control and merely quieter about it.

It is not hypothetical: a document parameter is an entire markdown file, and there is no way to paste one into a single-line box.
`format` then decides only whether the box opens at one row or eight.

### `format` is free to declare and cannot break anything

JSON Schema requires validators to ignore `format` they do not recognise, so an author setting `format: markdown` risks nothing and no validator anywhere starts rejecting their schema.
That is what makes it a contract rather than a trick, and it is written down in [`docs/plugins.md`](../docs/plugins.md) so an author can rely on it.

Option 5 would work identically and buys nothing, at the cost of a keyword only this project understands.

### The browser is the only place a bad shape is caught

[ADR-0046](0046-plugins-can-ask.md) already records that Dusk relays a schema it does not validate.
Nothing between the form and the plugin checks the shape, so a form that submits the wrong type produces a refusal from the plugin with no indication which field was wrong.
The form therefore validates against the schema it was handed and refuses to submit, which is the only place that check can happen at all today.

### One renderer, because two would drift

Action parameters and an elicited answer are both a schema and a set of values.
They now share `useSchemaForm`, `SchemaForm` and `ParamInput` rather than each having a copy, for the reason this repository has already been bitten by twice: a second implementation never fails loudly, it drifts.

## Consequences

### Good

- A plugin can declare the shape its action actually needs and have the browser express it.
- Elicitation gets the same controls, so the surface where a human answers is the surface that works.
- `format` is a contract an author can rely on, written down rather than inferred.
- Two pre-existing defects fell out of doing this: `.err` had no CSS rule at all, so **every error message in the application rendered as ordinary text**, and `.plugin-field input` was stretching checkboxes across the full width.

### Bad

- **An array of objects is still a JSON box.** A skeleton in the placeholder makes it fillable rather than pleasant. A repeatable-row widget was rejected because it would remove the paste path, which is the only thing making a large value survivable, but this is the weakest part of the decision.
- An item in a lines control cannot contain a newline. An array whose items declare `format` falls back to JSON, so the shape stays expressible, but the author has to know that.
- Objects render one level deep. Deeper is a JSON box, because a form that renders arbitrary depth is a schema editor and nobody asked for one.
- The form now validates, so it can be wrong in a new way: a schema Dusk misreads becomes a field somebody cannot submit, where previously it would at least reach the plugin.
- There are no component tests in this repository, so this is covered by type checking and by exercising the real schemas rather than by tests that will catch a regression.

### Rejected because

- **Option 1** was rejected because it pushes parsing into every plugin and makes the declared schema a lie, which is what produced the bug.
- **Option 2** was rejected because length is not the question. A 500 character URL is not prose and a 40 character sentence may be, and an author cannot rely on a threshold they cannot see.
- **Option 3** was rejected because guessing from a description is exactly the trick this is replacing, and it fails silently when somebody rewords their help text.
- **Option 5** was rejected as equivalent to option 4 but understood by nothing else.

## Amendments

### 2026-08-13: the form validates shape, and completeness is the plugin's

The decision above says the form "validates against the schema it was handed and refuses to submit".
Written that way it enforced two different things, and one of them was not its business.

Refusing a **shape** the plugin cannot accept is right: an unparseable JSON box or a string where an array belongs is something the browser knows and the plugin can only refuse less clearly.

Refusing an **incomplete** form is wrong, and it broke the feature it sits next to.
[ADR-0046](0046-plugins-can-ask.md) exists so a plugin can ask for what it was not given.
A form that will not submit until every required field is filled means the plugin is never invoked, never notices anything missing, and therefore can never ask.
The one surface where a human is present to answer an elicitation was the one surface that could not trigger one.

Required fields are still marked, and the form still refuses a bad shape.
Whether enough was supplied is answered by the plugin, which is the only thing that knows.

Found by trying to use it: the first attempt to test elicitation in the browser could not press Run.
