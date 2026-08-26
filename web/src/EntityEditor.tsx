import { useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent, KeyboardEvent as ReactKeyboardEvent } from "react";

import { api } from "./api";
import type { DeclarationChange, EntityDetail, EntitySource, WriteResult } from "./api";
import { handle } from "./App";

type Attribute = { key: string; value: string };

export function EntityEditor({
  entityRef,
  sources,
  onClose,
  onChanged,
}: {
  entityRef: string;
  sources: EntitySource[];
  onClose: () => void;
  onChanged: () => void;
}) {
  const repositories = useMemo(
    () => [...new Set(sources.filter((source) => !source.Observed).map((source) => source.Repository))],
    [sources],
  );
  const [repository, setRepository] = useState(repositories[0] ?? "");
  const [detail, setDetail] = useState<EntityDetail>();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [attributes, setAttributes] = useState<Attribute[]>([]);
  const [observedAs, setObservedAs] = useState("");
  const [decommissioned, setDecommissioned] = useState(false);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string>();
  const [result, setResult] = useState<WriteResult>();
  const [confirmingRemoval, setConfirmingRemoval] = useState(false);
  const editorRef = useRef<HTMLFormElement>(null);

  useEffect(() => {
    const close = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busy) {
        onClose();
      }
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [busy, onClose]);

  useEffect(() => {
    if (!repository) {
      return;
    }
    let live = true;
    setBusy(true);
    setProblem(undefined);
    setResult(undefined);
    void api
      .declaration(entityRef, repository)
      .then((next) => {
        if (!live) {
          return;
        }
        setDetail(next);
        setTitle(next.entity.title ?? "");
        setDescription(next.entity.description ?? "");
        setAttributes(
          Object.entries(next.entity.attributes ?? {})
            .filter(([key]) => key !== "lifecycle")
            .map(([key, value]) => ({ key, value: String(value) })),
        );
        setObservedAs((next.observed_as ?? []).join("\n"));
        setDecommissioned(next.entity.attributes?.lifecycle === "decommissioned");
      })
      .catch(handle((message) => live && setProblem(message)))
      .finally(() => live && setBusy(false));
    return () => {
      live = false;
    };
  }, [entityRef, repository]);

  useEffect(() => {
    if (detail) {
      editorRef.current?.querySelector<HTMLElement>("input, select, textarea")?.focus();
    }
  }, [detail]);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!detail?.proof) {
      setProblem("Dusk did not issue a proof for this declaration. Refresh and try again.");
      return;
    }

    const before = stringAttributes(detail);
    const entries = attributes
      .map((attribute) => [attribute.key.trim(), attribute.value] as const)
      .filter(([key]) => key);
    const keys = entries.map(([key]) => key);
    if (new Set(keys).size !== keys.length) {
      setProblem("Attribute names must be unique.");
      return;
    }
    if (keys.includes("lifecycle")) {
      setProblem("Use the Lifecycle field instead of a lifecycle attribute.");
      return;
    }
    const after = Object.fromEntries(entries);
    const unset = Object.keys(before)
      .filter((key) => !(key in after))
      .map((key) => `attributes.${key}`);
    const nextTitle = title.trim();
    const nextDescription = description;
    if ((detail.entity.title ?? "") && !nextTitle) {
      unset.push("title");
    }
    if ((detail.entity.description ?? "") && !nextDescription.trim()) {
      unset.push("description");
    }

    const change: DeclarationChange = {
      proof: detail.proof,
      repository,
      attributes: Object.fromEntries(
        Object.entries(after).filter(([key, value]) => before[key] !== value),
      ),
      observed_as: lines(observedAs),
      unset,
      decommissioned,
    };
    if (nextTitle && nextTitle !== (detail.entity.title ?? "")) {
      change.title = nextTitle;
    }
    if (nextDescription.trim() && nextDescription !== (detail.entity.description ?? "")) {
      change.description = nextDescription;
    }

    setBusy(true);
    setProblem(undefined);
    setResult(undefined);
    try {
      const answer = await api.updateEntity(entityRef, change);
      setResult(answer);
      if (!answer.proposed) {
        onChanged();
      }
    } catch (error) {
      handle(setProblem)(error);
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!detail?.proof) {
      return;
    }
    setBusy(true);
    setProblem(undefined);
    try {
      const answer = await api.updateEntity(entityRef, {
        proof: detail.proof,
        repository,
        remove: true,
        confirm: true,
      });
      setResult(answer);
      if (!answer.proposed) {
        onChanged();
      }
    } catch (error) {
      handle(setProblem)(error);
    } finally {
      setBusy(false);
      setConfirmingRemoval(false);
    }
  };

  const source = sources.find(
    (candidate) => !candidate.Observed && candidate.Repository === repository,
  );
  const committed = Boolean(result && !result.proposed);

  const keepFocus = (event: ReactKeyboardEvent<HTMLFormElement>) => {
    if (event.key !== "Tab") {
      return;
    }
    const controls = [...(editorRef.current?.querySelectorAll<HTMLElement>(
      "button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href]",
    ) ?? [])].filter((control) => control.offsetParent !== null);
    const first = controls[0];
    const last = controls.at(-1);
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last?.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first?.focus();
    }
  };

  return (
    <div className="editor-backdrop">
      <form
        ref={editorRef}
        className="note-editor entity-editor"
        role="dialog"
        aria-modal="true"
        aria-labelledby="entity-editor-title"
        onSubmit={submit}
        onKeyDown={keepFocus}
      >
        <header>
          <div>
            <p className="eyebrow">Edit declaration</p>
            <h2 id="entity-editor-title">{entityRef}</h2>
          </div>
          <button type="button" className="editor-close" aria-label="Close editor" onClick={onClose}>×</button>
        </header>

        <p className="repository-editor-help">
          Identity stays fixed. Rename a ref by declaring the new identity and moving its relationships.
        </p>

        {repositories.length > 1 && (
          <label className="entity-source-picker">
            <span>Declaring repository</span>
            <select value={repository} onChange={(event) => setRepository(event.target.value)}>
              {repositories.map((candidate) => <option key={candidate}>{candidate}</option>)}
            </select>
          </label>
        )}

        {problem && <p className="problem" role="alert">{problem}</p>}

        {detail && (
          <div className="note-editor-fields entity-editor-fields">
            <label>
              <span>Title</span>
              <input autoFocus value={title} onChange={(event) => setTitle(event.target.value)} />
            </label>
            <label>
              <span>Lifecycle</span>
              <select value={decommissioned ? "decommissioned" : "active"} onChange={(event) => setDecommissioned(event.target.value === "decommissioned")}>
                <option value="active">Active</option>
                <option value="decommissioned">Decommissioned</option>
              </select>
            </label>
            <label className="entity-description">
              <span>Description (Markdown)</span>
              <textarea value={description} onChange={(event) => setDescription(event.target.value)} />
            </label>
            <label className="entity-aliases">
              <span>Observed as, one entity ref per line</span>
              <textarea value={observedAs} onChange={(event) => setObservedAs(event.target.value)} placeholder="service:scope/name" />
            </label>

            <fieldset className="entity-attributes">
              <legend>Attributes</legend>
              {attributes.map((attribute, index) => (
                <div className="entity-attribute" key={index}>
                  <input aria-label={`Attribute ${index + 1} name`} placeholder="name" value={attribute.key} onChange={(event) => setAttributes(replaceAttribute(attributes, index, "key", event.target.value))} />
                  <input aria-label={`Attribute ${index + 1} value`} placeholder="value" value={attribute.value} onChange={(event) => setAttributes(replaceAttribute(attributes, index, "value", event.target.value))} />
                  <button type="button" className="btn secondary" aria-label={`Remove ${attribute.key || `attribute ${index + 1}`}`} onClick={() => setAttributes(attributes.filter((_, candidate) => candidate !== index))}>Remove</button>
                </div>
              ))}
              <button type="button" className="btn secondary" onClick={() => setAttributes([...attributes, { key: "", value: "" }])}>Add attribute</button>
            </fieldset>
          </div>
        )}

        {result && <WriteOutcome result={result} />}

        <footer>
          <button className="btn" type="submit" disabled={busy || !detail || committed}>{busy ? "Saving..." : committed ? "Saved" : "Save entity"}</button>
          <button className="btn secondary" type="button" onClick={onClose}>Close</button>
        </footer>

        {source?.Path !== "dusk.md" && detail && !committed && (
          <section className="entity-danger" aria-label="Remove declaration">
            <strong>Remove this declaration</strong>
            <p>This deletes {source?.Path} and its outbound relationships. Decommissioning keeps the history.</p>
            {confirmingRemoval ? (
              <div>
                <button className="btn danger" type="button" disabled={busy} onClick={() => void remove()}>Confirm removal</button>
                <button className="btn secondary" type="button" onClick={() => setConfirmingRemoval(false)}>Keep entity</button>
              </div>
            ) : (
              <button className="btn secondary" type="button" onClick={() => setConfirmingRemoval(true)}>Remove declaration</button>
            )}
          </section>
        )}
      </form>
    </div>
  );
}

function stringAttributes(detail: EntityDetail): Record<string, string> {
  return Object.fromEntries(
    Object.entries(detail.entity.attributes ?? {})
      .filter(([key]) => key !== "lifecycle")
      .map(([key, value]) => [key, String(value)]),
  );
}

function lines(value: string): string[] {
  return [...new Set(value.split("\n").map((line) => line.trim()).filter(Boolean))];
}

function replaceAttribute(attributes: Attribute[], index: number, field: keyof Attribute, value: string): Attribute[] {
  return attributes.map((attribute, candidate) => candidate === index ? { ...attribute, [field]: value } : attribute);
}

function WriteOutcome({ result }: { result: WriteResult }) {
  if (result.proposed) {
    return (
      <div className="context-proposal" role="status">
        <p>Dusk cannot commit to {result.repository}. Apply this proposal to {result.path}.</p>
        <pre>{result.diff}</pre>
      </div>
    );
  }
  return (
    <p className="entity-write-success" role="status">
      {result.removed ? "Declaration removed." : "Declaration saved."}{" "}
      {result.url && <a href={result.url} target="_blank" rel="noreferrer">View commit ↗</a>}
    </p>
  );
}
