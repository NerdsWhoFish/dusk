import { useState } from "react";

import { NeedsApproval, api } from "./api";
import type { Action, Answered, Ask, Invocation, Outcome } from "./api";

// Actions renders what a plugin declares as buttons. Nothing here knows about
// any plugin: the same ActionDescriptor is a button, an invocable capability
// over MCP, and an approval gate (ADR-0041).
export function Actions({
  actions,
  entityRef,
  proof,
  onRan,
}: {
  actions: Action[];
  entityRef?: string;
  proof?: string;
  onRan?: () => void;
}) {
  const offered = actions.filter((action) => action.enabled);
  const [open, setOpen] = useState<string>();

  if (offered.length === 0) {
    return null;
  }

  return (
    <>
      <h2>Actions</h2>
      <div className="actions">
        {offered.map((action) => (
          <ActionCard
            key={`${action.plugin}/${action.name}`}
            action={action}
            entityRef={entityRef}
            proof={proof}
            open={open === action.name}
            onToggle={() => setOpen(open === action.name ? undefined : action.name)}
            onRan={onRan}
          />
        ))}
      </div>
    </>
  );
}

function ActionCard({
  action,
  entityRef,
  proof,
  open,
  onToggle,
  onRan,
}: {
  action: Action;
  entityRef?: string;
  proof?: string;
  open: boolean;
  onToggle: () => void;
  onRan?: () => void;
}) {
  const fields = schemaFields(action.params);
  const form = useSchemaForm(fields);

  const [outcome, setOutcome] = useState<Outcome>();
  const [asking, setAsking] = useState<string>();
  const [problem, setProblem] = useState<string>();
  const [busy, setBusy] = useState(false);

  // What the invocation was, so answering a question resumes that action rather
  // than starting a fresh one with different arguments.
  const [resuming, setResuming] = useState<Invocation>();

  const run = async (extra: Invocation) => {
    setBusy(true);
    setProblem(undefined);
    setAsking(undefined);
    try {
      const body: Invocation = {
        params: form.values,
        proof,
        plugin: action.plugin,
        ...extra,
      };
      setResuming(body);
      const answer = entityRef
        ? await api.invoke(entityRef, action.name, body)
        : await api.invokePlugin(action.plugin, action.name, body);

      setOutcome(answer);
      if (!extra.preview && answer.ok) {
        onRan?.();
      }
    } catch (error) {
      // Being asked to agree is a question, so it gets the confirm affordance
      // rather than the error styling somebody has to read past.
      if (error instanceof NeedsApproval) {
        setAsking(error.message);
      } else {
        setProblem(error instanceof Error ? error.message : String(error));
      }
    } finally {
      setBusy(false);
    }
  };

  const needsForm = fields.length > 0;

  // Nothing is sent while the form is known to be wrong, so a shape the plugin
  // would refuse is refused here, next to the field that is wrong.
  const send = (extra: Invocation) => {
    if (form.ready()) {
      void run(extra);
    }
  };

  return (
    <section className={`action action-${action.class}`}>
      <header className="action-head">
        <div className="action-title">
          <strong>{title(action.name)}</strong>
          <span className={`tag class-${action.class}`}>{label(action.class)}</span>
          <span className="quiet">{action.plugin}</span>
        </div>
        <div className="action-buttons">
          {needsForm && (
            <button type="button" className="btn secondary" onClick={onToggle}>
              {open ? "Cancel" : "Set up"}
            </button>
          )}
          {!needsForm && (
            <button
              type="button"
              className="btn"
              disabled={busy}
              onClick={() => run({})}
            >
              {busy ? "Running" : "Run"}
            </button>
          )}
        </div>
      </header>

      <p className="hint">{action.description}</p>

      {open && needsForm && (
        <div className="action-form">
          <SchemaForm
            fields={fields}
            values={form.values}
            problems={form.problems}
            onChange={form.change}
          />

          <div className="action-buttons">
            <button
              type="button"
              className="btn secondary"
              disabled={busy}
              onClick={() => send({ preview: true })}
            >
              Preview
            </button>
            <button type="button" className="btn" disabled={busy} onClick={() => send({})}>
              {busy ? "Running" : "Run"}
            </button>
          </div>
        </div>
      )}

      {asking && (
        <div className="action-confirm" role="alert">
          <p>{asking}</p>
          <button type="button" className="btn danger" onClick={() => run({ confirm: true })}>
            Yes, do it
          </button>
        </div>
      )}

      {problem && (
        <p className="hint err" role="alert">
          {problem}
        </p>
      )}

      {outcome?.ask && (
        <Question
          ask={outcome.ask}
          busy={busy}
          onAnswer={(answer) => run({ ...resuming, elicited: answer })}
        />
      )}

      {outcome && !outcome.ask && (
        <Result outcome={outcome} plugin={action.plugin} />
      )}
    </section>
  );
}

function Result({ outcome, plugin }: { outcome: Outcome; plugin: string }) {
  const [polled, setPolled] = useState<Outcome>();
  const shown = polled ?? outcome;

  return (
    <div className={`action-result ${shown.ok ? "ok" : "bad"}`}>
      <p>{shown.previewed ? `Would ${shown.preview}` : shown.message}</p>

      {shown.handle && !shown.done && (
        <button
          type="button"
          className="btn secondary"
          onClick={() => {
            api
              .handle(plugin, shown.handle as string)
              .then(setPolled)
              .catch(() => undefined);
          }}
        >
          Check
        </button>
      )}

      {(shown.steps ?? []).length > 0 && (
        <ul className="action-steps">
          {shown.steps?.map((step, index) => (
            <li key={`${step.action}-${index}`} className={step.ok ? "ok" : "bad"}>
              <strong>{title(step.action)}</strong> {step.message}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

type Schema = Record<string, unknown>;

// Control is how a field is edited, which is not its JSON Schema type: an array
// of objects and an open map are both a JSON box, and every string is a
// textarea because a JSON Schema string may contain newlines.
type Control =
  | "text"
  | "prose"
  | "number"
  | "check"
  | "choice"
  | "lines"
  | "json"
  | "group";

type ParamField = {
  name: string;
  control: Control;
  description?: string;
  required: boolean;
  options?: string[];
  item?: Schema;
  fields?: ParamField[];
  schema: Schema;
};

type Problems = Record<string, string>;

const needed = "this one is needed";

// useSchemaForm holds what a form has been given and what is wrong with it.
// Dusk relays a schema it does not validate (ADR-0046), so the browser is the
// only place a bad shape is caught before the plugin refuses it.
function useSchemaForm(fields: ParamField[]) {
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [problems, setProblems] = useState<Problems>({});

  // An empty box drops its key rather than sending "", so a plugin can tell
  // "not given" from "given as nothing", the same reason numberOf exists.
  const change = (name: string, next: unknown, problem?: string) => {
    setValues((was) => {
      const now = { ...was };
      if (next === undefined) {
        delete now[name];
      } else {
        now[name] = next;
      }
      return now;
    });

    setProblems((was) => {
      const now = { ...was };
      if (problem) {
        now[name] = problem;
      } else {
        delete now[name];
      }
      return now;
    });
  };

  // ready refuses a form already known to be wrong, and says why against the
  // field rather than leaving the plugin to say it less clearly.
  const ready = (): boolean => {
    const found: Problems = { ...problems };
    for (const field of fields) {
      // An unticked box is an answer, so a boolean is never missing.
      if (field.control === "check" || !field.required) {
        continue;
      }
      // Never over a reason already given: a box that did not parse is empty,
      // and "this one is needed" is the less useful of the two things to say.
      if (blank(values[field.name])) {
        found[field.name] ??= needed;
      }
    }

    setProblems(found);
    return Object.keys(found).length === 0;
  };

  return { values, problems, change, ready };
}

// SchemaForm is the one renderer for a declared shape: an action's parameters
// and a question asked mid-action are the same schema in the same boxes.
function SchemaForm({
  fields,
  values,
  problems,
  onChange,
}: {
  fields: ParamField[];
  values: Record<string, unknown>;
  problems: Problems;
  onChange: (name: string, next: unknown, problem?: string) => void;
}) {
  return (
    <>
      {fields.map((field) => (
        <ParamInput
          key={field.name}
          field={field}
          value={values[field.name]}
          problem={problems[field.name]}
          onChange={(next, problem) => onChange(field.name, next, problem)}
        />
      ))}
    </>
  );
}

// Question renders what a plugin asked for mid-action. The action has not run:
// answering resumes it, and declining tells the plugin so rather than silently
// abandoning it, since a considered no is something it may act on (ADR-0046).
function Question({
  ask,
  busy,
  onAnswer,
}: {
  ask: Ask;
  busy: boolean;
  onAnswer: (answer: Answered) => void;
}) {
  const fields = schemaFields(ask.schema);
  const form = useSchemaForm(fields);

  return (
    <div className="action-form action-asking">
      <p className="hint">{ask.prompt}</p>

      <SchemaForm
        fields={fields}
        values={form.values}
        problems={form.problems}
        onChange={form.change}
      />

      <div className="action-buttons">
        <button
          type="button"
          className="btn"
          disabled={busy}
          onClick={() => {
            if (form.ready()) {
              onAnswer({ outcome: "accept", values: form.values, token: ask.token });
            }
          }}
        >
          Answer
        </button>
        <button
          type="button"
          className="btn secondary"
          disabled={busy}
          onClick={() => onAnswer({ outcome: "decline", token: ask.token })}
        >
          Decline
        </button>
      </div>
    </div>
  );
}

// schemaFields reads a JSON Schema into a flat form. One level of nesting is
// expanded into a group; deeper than that is a JSON box, because a form that
// renders arbitrary depth is a schema editor.
function schemaFields(schema: Schema | undefined, depth = 0): ParamField[] {
  const properties = schema?.properties as Record<string, Schema> | undefined;
  if (!properties) {
    return [];
  }

  const required = new Set((schema?.required as string[]) ?? []);
  return Object.entries(properties).map(([name, property]) => {
    const control = controlFor(property, depth);
    return {
      name,
      control,
      description: property.description as string | undefined,
      required: required.has(name),
      options: (property.enum as unknown[] | undefined)?.map(String),
      item: property.items as Schema | undefined,
      fields: control === "group" ? schemaFields(property, depth + 1) : undefined,
      schema: property,
    };
  });
}

function controlFor(property: Schema, depth: number): Control {
  if (property.enum) {
    return "choice";
  }

  switch (String(property.type ?? "string")) {
    case "boolean":
      return "check";
    case "integer":
    case "number":
      return "number";
    case "array":
      return scalarItems(property.items as Schema | undefined) ? "lines" : "json";
    case "object":
      return depth === 0 && declared(property) ? "group" : "json";
    default:
      return multiline(property) ? "prose" : "text";
  }
}

// declared separates an object whose fields are known from an open map, which
// has nothing to render a field from and gets the JSON box instead.
function declared(schema: Schema): boolean {
  const properties = schema.properties as Record<string, Schema> | undefined;
  return Boolean(properties) && Object.keys(properties as object).length > 0;
}

// scalarItems reports whether a list can be written a line at a time. An item
// that is itself multi-line cannot, because the newline is the separator.
function scalarItems(item: Schema | undefined): boolean {
  switch (String(item?.type ?? "")) {
    case "string":
      return !multiline(item);
    case "integer":
    case "number":
      return true;
    default:
      return false;
  }
}

// multiline is how a plugin says a string is prose. format is an annotation
// JSON Schema validators ignore, so declaring it costs an author nothing.
// The contract is written down in docs/plugins.md.
function multiline(schema: Schema | undefined): boolean {
  const format = String(schema?.format ?? "");
  return (
    format === "textarea" ||
    format === "markdown" ||
    String(schema?.contentMediaType ?? "") === "text/markdown"
  );
}

// blank is what "the person did not fill this in" means for every shape a
// field can hold, which is not the same as falsy: 0 and false are answers.
function blank(value: unknown): boolean {
  if (value === undefined || value === null || value === "") {
    return true;
  }
  if (Array.isArray(value)) {
    return value.length === 0;
  }
  return typeof value === "object" && Object.keys(value as object).length === 0;
}

// example builds the skeleton shown in an empty JSON box. Pasting a shape is
// the difference between a plugin's complaint and a filled form.
function example(schema: Schema | undefined, depth = 0): unknown {
  if (!schema || depth > 3) {
    return null;
  }

  const options = schema.enum as unknown[] | undefined;
  if (options && options.length > 0) {
    return options[0];
  }

  switch (String(schema.type ?? "")) {
    case "string":
      return "";
    case "integer":
    case "number":
      return 0;
    case "boolean":
      return false;
    case "array":
      return [example(schema.items as Schema | undefined, depth + 1)];
    case "object": {
      const properties = schema.properties as Record<string, Schema> | undefined;
      if (properties) {
        const shape: Record<string, unknown> = {};
        for (const [name, property] of Object.entries(properties)) {
          shape[name] = example(property, depth + 1);
        }
        return shape;
      }

      const rest = schema.additionalProperties;
      return rest && typeof rest === "object"
        ? { key: example(rest as Schema, depth + 1) }
        : {};
    }
    default:
      return null;
  }
}

function ParamInput({
  field,
  value,
  problem,
  prefix = "param",
  onChange,
}: {
  field: ParamField;
  value: unknown;
  problem?: string;
  prefix?: string;
  onChange: (next: unknown, problem?: string) => void;
}) {
  const id = `${prefix}-${field.name}`;

  if (field.control === "check") {
    return (
      <label className="plugin-field check" htmlFor={id}>
        <input
          id={id}
          type="checkbox"
          checked={Boolean(value)}
          onChange={(e) => onChange(e.target.checked)}
        />
        <span>{title(field.name)}</span>
        {field.description && <span className="hint">{field.description}</span>}
      </label>
    );
  }

  if (field.control === "group") {
    return (
      <GroupInput
        id={id}
        field={field}
        value={value}
        problem={problem}
        onChange={onChange}
      />
    );
  }

  return (
    <label className="plugin-field" htmlFor={id}>
      <span className="plugin-field-label">
        {title(field.name)}
        {field.required && <span className="req"> required</span>}
      </span>

      <FieldControl
        id={id}
        field={field}
        value={value}
        invalid={Boolean(problem)}
        onChange={onChange}
      />

      {field.description && <span className="hint">{field.description}</span>}
      {problem && (
        <span className="hint err" role="alert">
          {problem}
        </span>
      )}
    </label>
  );
}

function FieldControl({
  id,
  field,
  value,
  invalid,
  onChange,
}: {
  id: string;
  field: ParamField;
  value: unknown;
  invalid: boolean;
  onChange: (next: unknown, problem?: string) => void;
}) {
  switch (field.control) {
    case "choice":
      return (
        <select
          id={id}
          aria-invalid={invalid}
          value={String(value ?? "")}
          onChange={(e) => onChange(e.target.value === "" ? undefined : e.target.value)}
        >
          <option value="">Choose one</option>
          {(field.options ?? []).map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </select>
      );

    case "number":
      return (
        <input
          id={id}
          type="number"
          aria-invalid={invalid}
          value={value === undefined ? "" : String(value)}
          onChange={(e) => onChange(numberOf(e.target.value))}
        />
      );

    case "lines":
      return <LinesInput id={id} field={field} value={value} invalid={invalid} onChange={onChange} />;

    case "json":
      return <JsonInput id={id} field={field} value={value} invalid={invalid} onChange={onChange} />;

    default:
      return (
        <textarea
          id={id}
          rows={field.control === "prose" ? 8 : 1}
          aria-invalid={invalid}
          value={String(value ?? "")}
          onChange={(e) => onChange(e.target.value === "" ? undefined : e.target.value)}
        />
      );
  }
}

// LinesInput takes a list a line at a time, which is how the shapes this
// renders are already written in prose, and is the only editor a list of
// strings needs. A blank line is spacing, never an empty item.
function LinesInput({
  id,
  field,
  value,
  invalid,
  onChange,
}: {
  id: string;
  field: ParamField;
  value: unknown;
  invalid: boolean;
  onChange: (next: unknown, problem?: string) => void;
}) {
  const [raw, setRaw] = useState(() =>
    Array.isArray(value) ? value.map(String).join("\n") : "",
  );

  const edit = (text: string) => {
    setRaw(text);
    const lines = text
      .split("\n")
      .map((line) => line.trim())
      .filter((line) => line !== "");

    if (lines.length === 0) {
      onChange(undefined);
      return;
    }

    const item = field.item;
    const wanted = String(item?.type ?? "string");
    if (wanted === "string") {
      const allowed = (item?.enum as unknown[] | undefined)?.map(String);
      const wrong = allowed && lines.find((line) => !allowed.includes(line));
      if (wrong) {
        onChange(undefined, `"${wrong}" is not one of ${allowed?.join(", ")}`);
        return;
      }
      onChange(lines);
      return;
    }

    const numbers: number[] = [];
    for (const line of lines) {
      const parsed = Number(line);
      if (!Number.isFinite(parsed) || (wanted === "integer" && !Number.isInteger(parsed))) {
        onChange(undefined, `"${line}" is not ${wanted === "integer" ? "a whole number" : "a number"}`);
        return;
      }
      numbers.push(parsed);
    }
    onChange(numbers);
  };

  return (
    <textarea
      id={id}
      rows={4}
      aria-invalid={invalid}
      placeholder="One per line"
      value={raw}
      onChange={(e) => edit(e.target.value)}
    />
  );
}

// JsonInput is the last resort, for a shape no single control expresses: a list
// of objects, or an open map. It is deliberately paste-shaped, because that
// data is produced by something else far more often than it is typed.
function JsonInput({
  id,
  field,
  value,
  invalid,
  onChange,
}: {
  id: string;
  field: ParamField;
  value: unknown;
  invalid: boolean;
  onChange: (next: unknown, problem?: string) => void;
}) {
  const [raw, setRaw] = useState(() =>
    value === undefined ? "" : JSON.stringify(value, null, 2),
  );

  const edit = (text: string) => {
    setRaw(text);
    if (text.trim() === "") {
      onChange(undefined);
      return;
    }

    let parsed: unknown;
    try {
      parsed = JSON.parse(text);
    } catch (error) {
      onChange(undefined, `that is not JSON: ${error instanceof Error ? error.message : error}`);
      return;
    }

    const wanted = String(field.schema.type ?? "");
    if (wanted === "array" && !Array.isArray(parsed)) {
      onChange(undefined, "this one takes a JSON array");
      return;
    }
    if (wanted === "object" && (parsed === null || typeof parsed !== "object" || Array.isArray(parsed))) {
      onChange(undefined, "this one takes a JSON object");
      return;
    }
    onChange(parsed);
  };

  return (
    <textarea
      id={id}
      className="code"
      rows={8}
      aria-invalid={invalid}
      placeholder={JSON.stringify(example(field.schema), null, 2)}
      value={raw}
      onChange={(e) => edit(e.target.value)}
    />
  );
}

// GroupInput renders a declared object as its own small form. Its content is
// often a whole file, and a document typed into a JSON string literal with
// escaped newlines is not something anybody should be asked to do.
function GroupInput({
  id,
  field,
  value,
  problem,
  onChange,
}: {
  id: string;
  field: ParamField;
  value: unknown;
  problem?: string;
  onChange: (next: unknown, problem?: string) => void;
}) {
  const [parsing, setParsing] = useState<Problems>({});

  const nested = field.fields ?? [];
  const current = (value ?? {}) as Record<string, unknown>;
  const shown = nestedProblems(nested, current, parsing);

  const change = (name: string, next: unknown, why?: string) => {
    const now = { ...current };
    if (next === undefined) {
      delete now[name];
    } else {
      now[name] = next;
    }

    const found = { ...parsing };
    if (why) {
      found[name] = why;
    } else {
      delete found[name];
    }
    setParsing(found);

    // The problem is reported even when the object empties out, or clearing the
    // last field would unblock the form while its error is still on screen.
    onChange(
      Object.keys(now).length === 0 ? undefined : now,
      firstProblem(nested, nestedProblems(nested, now, found)),
    );
  };

  return (
    <fieldset className="plugin-field plugin-group">
      <legend className="plugin-field-label">
        {title(field.name)}
        {field.required && <span className="req"> required</span>}
      </legend>

      {field.description && <span className="hint">{field.description}</span>}
      {problem && Object.keys(shown).length === 0 && (
        <span className="hint err" role="alert">
          {problem}
        </span>
      )}

      <div className="plugin-group-fields">
        {nested.map((one) => (
          <ParamInput
            key={one.name}
            prefix={id}
            field={one}
            value={current[one.name]}
            problem={shown[one.name]}
            onChange={(next, why) => change(one.name, next, why)}
          />
        ))}
      </div>
    </fieldset>
  );
}

// nestedProblems adds what an object is missing, but only once it exists at
// all: naming every required field of an untouched object is nagging, not help.
function nestedProblems(
  fields: ParamField[],
  value: Record<string, unknown>,
  parsing: Problems,
): Problems {
  const shown = { ...parsing };
  if (Object.keys(value).length === 0) {
    return shown;
  }

  for (const field of fields) {
    if (field.required && field.control !== "check" && blank(value[field.name])) {
      shown[field.name] ??= needed;
    }
  }
  return shown;
}

// firstProblem is what an object reports upward, so a group blocks the form
// even though its own message is shown against the field inside it.
function firstProblem(fields: ParamField[], problems: Problems): string | undefined {
  for (const field of fields) {
    const problem = problems[field.name];
    if (problem) {
      return `${title(field.name)}: ${problem}`;
    }
  }
  return undefined;
}

// numberOf keeps an empty box out of the request as 0, which for a volume is
// the difference between "not set" and "silence".
function numberOf(raw: string): number | undefined {
  return raw === "" ? undefined : Number(raw);
}

function title(name: string): string {
  const words = name.replace(/_/g, " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

function label(actionClass: string): string {
  return actionClass.replace(/_/g, " ");
}
