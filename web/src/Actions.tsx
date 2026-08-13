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
  const [params, setParams] = useState<Record<string, unknown>>({});
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
      const body: Invocation = { params, proof, plugin: action.plugin, ...extra };
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

  const fields = schemaFields(action.params);
  const needsForm = fields.length > 0;

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
          {fields.map((field) => (
            <ParamInput
              key={field.name}
              field={field}
              value={params[field.name]}
              onChange={(next) =>
                setParams((was) => ({ ...was, [field.name]: next }))
              }
            />
          ))}

          <div className="action-buttons">
            <button
              type="button"
              className="btn secondary"
              disabled={busy}
              onClick={() => run({ preview: true })}
            >
              Preview
            </button>
            <button type="button" className="btn" disabled={busy} onClick={() => run({})}>
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

type ParamField = {
  name: string;
  type: string;
  description?: string;
  required: boolean;
  options?: string[];
};

// schemaFields reads an action's JSON Schema into a flat form. Only the shapes
// a form can render are taken; anything nested is left to an agent, which does
// not need a form at all.
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
  const [values, setValues] = useState<Record<string, unknown>>({});
  const fields = schemaFields(ask.schema);

  return (
    <div className="action-form action-asking">
      <p className="hint">{ask.prompt}</p>

      {fields.map((field) => (
        <ParamInput
          key={field.name}
          field={field}
          value={values[field.name]}
          onChange={(next) =>
            setValues((was) => ({ ...was, [field.name]: next }))
          }
        />
      ))}

      <div className="action-buttons">
        <button
          type="button"
          className="btn"
          disabled={busy}
          onClick={() => onAnswer({ outcome: "accept", values, token: ask.token })}
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

function schemaFields(schema: Record<string, unknown> | undefined): ParamField[] {
  const properties = schema?.properties as Record<string, Record<string, unknown>>;
  if (!properties) {
    return [];
  }

  const required = new Set((schema?.required as string[]) ?? []);
  return Object.entries(properties).map(([name, property]) => ({
    name,
    type: String(property.type ?? "string"),
    description: property.description as string | undefined,
    required: required.has(name),
    options: property.enum as string[] | undefined,
  }));
}

function ParamInput({
  field,
  value,
  onChange,
}: {
  field: ParamField;
  value: unknown;
  onChange: (next: unknown) => void;
}) {
  const id = `param-${field.name}`;

  if (field.type === "boolean") {
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

  return (
    <label className="plugin-field" htmlFor={id}>
      <span className="plugin-field-label">
        {title(field.name)}
        {field.required && <span className="req"> required</span>}
      </span>

      {field.options ? (
        <select id={id} value={String(value ?? "")} onChange={(e) => onChange(e.target.value)}>
          <option value="">Choose one</option>
          {field.options.map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </select>
      ) : (
        <input
          id={id}
          type={field.type === "integer" || field.type === "number" ? "number" : "text"}
          value={String(value ?? "")}
          onChange={(e) =>
            onChange(
              field.type === "integer" || field.type === "number"
                ? numberOf(e.target.value)
                : e.target.value,
            )
          }
        />
      )}

      {field.description && <span className="hint">{field.description}</span>}
    </label>
  );
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
