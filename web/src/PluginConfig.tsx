import { useState } from "react";

import { api, type PluginConfig, type PluginField, type PluginOffer } from "./api";

// ConfigForm renders whatever a plugin says it needs, from the typed field list
// its Describe returns. Nothing here knows about any particular plugin.
export function ConfigForm({
  offer,
  instance,
  onSaved,
}: {
  offer: PluginOffer;
  instance?: string;
  onSaved: () => void;
}) {
  const current = instance ? (offer.instances?.[instance] ?? {}) : (offer.config ?? {});
  const set = new Set(offer.set?.[instance ?? ""] ?? []);
  const [values, setValues] = useState<PluginConfig>(current);
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState<string>();

  const fields = offer.fields ?? [];
  if (fields.length === 0) {
    return null;
  }

  const save = async () => {
    setSaving(true);
    setProblem(undefined);
    try {
      await api.configure(offer.id, values, instance);
      onSaved();
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="plugin-config">
      {fields.map((field) => (
        <FieldInput
          key={field.name}
          field={field}
          value={values[field.name]}
          filled={set.has(field.name)}
          onChange={(next) => setValues((was) => ({ ...was, [field.name]: next }))}
        />
      ))}

      {problem && (
        <p className="hint err" role="alert">
          {problem}
        </p>
      )}

      <button type="button" className="btn" disabled={saving} onClick={save}>
        {saving ? "Saving" : instance ? `Save ${instance}` : "Save"}
      </button>
    </div>
  );
}

function FieldInput({
  field,
  value,
  filled,
  onChange,
}: {
  field: PluginField;
  value: unknown;
  filled: boolean;
  onChange: (next: unknown) => void;
}) {
  const id = `${field.name}-field`;

  if (field.type === "bool") {
    return (
      <label className="plugin-field check" htmlFor={id}>
        <input
          id={id}
          type="checkbox"
          checked={Boolean(value)}
          onChange={(e) => onChange(e.target.checked)}
        />
        <span>{field.label || field.name}</span>
        {field.help && <span className="hint">{field.help}</span>}
      </label>
    );
  }

  return (
    <label className="plugin-field" htmlFor={id}>
      <span className="plugin-field-label">
        {field.label || field.name}
        {field.required && <span className="req"> required</span>}
      </span>
      <input
        id={id}
        // A sensitive value is entered here and never read back, so the field
        // starts empty rather than showing something that is not the secret.
        type={field.sensitive ? "password" : field.type === "int" ? "number" : "text"}
        value={field.sensitive ? undefined : String(value ?? "")}
        // Left empty, a stored credential stays. Without saying so, editing
        // any other field looks like it would clear this one.
        placeholder={field.sensitive && filled ? "set, leave empty to keep it" : undefined}
        autoComplete={field.sensitive ? "new-password" : "off"}
        onChange={(e) => onChange(e.target.value)}
      />
      {field.help && <span className="hint">{field.help}</span>}
    </label>
  );
}
