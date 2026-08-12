import { useEffect, useState } from "react";
import { api } from "./api";
import type { EntityDetail } from "./api";
import { handle } from "./App";
import { Markdown } from "./Markdown";
import { Rows } from "./Rows";

export function EntityView({
  entityRef,
  onOpen,
}: {
  entityRef: string;
  onOpen: (ref: string | null) => void;
}) {
  const [detail, setDetail] = useState<EntityDetail | null>(null);
  const [problem, setProblem] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    setDetail(null);
    setProblem(null);

    api
      .entity(entityRef)
      .then((data) => live && setDetail(data))
      .catch(handle((message) => live && setProblem(message)));

    return () => {
      live = false;
    };
  }, [entityRef]);

  const back = (
    <a
      className="back"
      href="/"
      onClick={(e) => {
        e.preventDefault();
        onOpen(null);
      }}
    >
      ← All entities
    </a>
  );

  if (problem) {
    return (
      <>
        {back}
        <p className="problem">{problem}</p>
      </>
    );
  }
  if (!detail) {
    return (
      <>
        {back}
        <div className="skeleton" style={{ height: "10rem" }} aria-hidden="true" />
      </>
    );
  }

  const { entity, relations, notes } = detail;
  const attributes = Object.entries(entity.attributes ?? {});

  return (
    <>
      {back}
      <div className="title">
        <h1>{entity.title || entity.name}</h1>
        <span className={`tag kind-${entity.kind}`}>{entity.kind}</span>
      </div>
      <p className="ref">{entity.ref}</p>

      {entity.description && <Markdown>{entity.description}</Markdown>}

      {notes.length > 0 && (
        <>
          <h2>Notes</h2>
          {notes.map((note) => (
            <article className="note" key={note.id}>
              <span className="tag note-kind">{note.kind}</span>
              <Markdown>{note.body}</Markdown>
              <p className="ref note-id">{note.id}</p>
            </article>
          ))}
        </>
      )}

      {attributes.length > 0 && (
        <>
          <h2>Attributes</h2>
          <dl className="attrs">
            {attributes.map(([key, value]) => (
              <div key={key} style={{ display: "contents" }}>
                <dt>{key}</dt>
                <dd>{renderValue(value)}</dd>
              </div>
            ))}
          </dl>
        </>
      )}

      {relations.length > 0 && (
        <>
          <h2>Connections</h2>
          <Rows
            items={relations.map((relation) => ({
              key: `${relation.type}:${relation.to}`,
              title: relation.to,
              mono: true,
              tag: relation.type,
              tagKind: "rel" as const,
              onOpen: () => onOpen(relation.to),
            }))}
            empty=""
          />
        </>
      )}

      {entity.provenance?.version && (
        <p className="ref" style={{ marginTop: "2rem" }}>
          Read from {entity.provenance.source} at {entity.provenance.version.slice(0, 7)}
        </p>
      )}
    </>
  );
}

// A url attribute is the most useful thing on the page for an operator, so it
// is a link rather than text they have to select and paste.
function renderValue(value: unknown) {
  const text = String(value);
  if (text.startsWith("https://") || text.startsWith("http://")) {
    return (
      <a href={text} rel="noreferrer">
        {text}
      </a>
    );
  }
  return text;
}
