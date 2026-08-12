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

      {/* The identity band answers "what is this" before any prose. It carries
          the horizon gradient so an entity page belongs to the same product
          as the landing rather than reading as a bare document. */}
      <header className="identity">
        <div className="title">
          <h1>{entity.title || entity.name}</h1>
          <span className={`tag kind-${entity.kind}`}>{entity.kind}</span>
        </div>
        <p className="ref">{entity.ref}</p>
        {url(entity.attributes) && (
          <a className="visit" href={url(entity.attributes)} rel="noreferrer">
            {url(entity.attributes)?.replace(/^https?:\/\//, "")}
          </a>
        )}
      </header>

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
            items={relations.map((relation) => {
              // Neighbors returns edges in both directions, so the row has to
              // show the far end. Rendering `to` unconditionally made an
              // entity with many inbound edges list itself once per edge.
              const inbound = relation.to === entity.ref;
              const other = inbound ? relation.from : relation.to;

              return {
                key: `${relation.type}:${relation.from}:${relation.to}`,
                title: other,
                mono: true,
                // The arrow carries the direction, so "runs_on" reads the
                // right way round from whichever end you are standing at.
                tag: inbound ? `← ${relation.type}` : `${relation.type} →`,
                tagKind: "rel" as const,
                onOpen: () => onOpen(other),
              };
            })}
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

// url is the single most useful thing on the page for an operator, so it is
// promoted out of the attribute list into the header.
function url(attributes: Record<string, unknown> | undefined): string | undefined {
  const value = String(attributes?.url ?? "");
  return value.startsWith("http") ? value : undefined;
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
