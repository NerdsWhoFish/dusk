import { useCallback, useEffect, useState } from "react";
import { Actions } from "./Actions";
import { api } from "./api";
import type { EntityDetail, EntitySource, Event } from "./api";
import { handle } from "./App";
import { Events } from "./Events";
import { Markdown } from "./Markdown";
import { Notes } from "./Notes";
import { PluginBlock } from "./PluginView";
import { Rows } from "./Rows";
import { describe, useVocabulary } from "./vocabulary";

export function EntityView({
  entityRef,
  onOpen,
}: {
  entityRef: string;
  onOpen: (ref: string | null) => void;
}) {
  const [detail, setDetail] = useState<EntityDetail | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const vocabulary = useVocabulary();

  // Reloading after an action is not a nicety: the answer carried the proof
  // token, and running something invalidates it along with what it described.
  const load = useCallback(() => {
    let live = true;
    setProblem(null);

    api
      .entity(entityRef)
      .then((data) => live && setDetail(data))
      .catch(handle((message) => live && setProblem(message)));

    return () => {
      live = false;
    };
  }, [entityRef]);

  useEffect(() => {
    setDetail(null);
    return load();
  }, [load]);

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
          {/* Only reference is shown. Infrastructure is the default and says
              nothing; reference says nobody is expected to declare these, which
              is why drift stays quiet about them (ADR-0048). */}
          {describe(entity.kind, vocabulary)?.role === "reference" && (
            <span className="tag reference">reference</span>
          )}
        </div>
        <p className="ref">{entity.ref}</p>
        {url(entity.attributes) && (
          <a className="visit" href={url(entity.attributes)} rel="noreferrer">
            {url(entity.attributes)?.replace(/^https?:\/\//, "")}
          </a>
        )}
      </header>

      <Briefing detail={detail} onOpen={onOpen} />

      {entity.description && <Markdown>{entity.description}</Markdown>}

      {/* Before the notes: a plugin's own view of a thing is usually the most
          specific thing on the page. */}
      {(detail.views ?? []).map((view) => (
        <PluginBlock
          key={view.source ?? `${view.plugin}-${view.title}`}
          view={view}
          entityRef={entity.ref}
          entities={[entity]}
          proof={detail.proof}
          onOpen={onOpen}
        />
      ))}

      <Actions
        actions={detail.actions ?? []}
        entityRef={entity.ref}
        proof={detail.proof}
        onRan={load}
      />

      {notes.length > 0 && (
        <>
          <h2>Notes</h2>
          <Notes notes={notes} proof={detail.proof} onChanged={load} />
        </>
      )}

      <h2>Operational history</h2>
      <Events recorded={detail.events ?? []} ref={entity.ref} />

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

      {!!detail.sources?.length && (
        <section aria-labelledby="provenance-heading">
          <h2 id="provenance-heading">Provenance</h2>
          {detail.sources.map((source) => (
            <p className="ref" key={`${source.Repository}:${source.Path}:${source.Source}`}>
              {source.Observed ? "Observed" : "Declared"} by{" "}
              {source.Observed ? source.Source : source.Repository}
              {source.Path ? ` in ${source.Path}` : ""}
              {source.Version ? ` at ${shortVersion(source.Version)}` : ""}
            </p>
          ))}
          {!detail.sources.some((source) => !source.Observed) && (
            <p className="ref">Observed only — no repository declares this entity.</p>
          )}
        </section>
      )}
    </>
  );
}

function Briefing({
  detail,
  onOpen,
}: {
  detail: EntityDetail;
  onOpen: (ref: string | null) => void;
}) {
  const truth = sourceTruth(detail.sources ?? []);
  const dependents = detail.dependents ?? [];
  const direct = dependents.filter((dependent) => dependent.Depth === 1).length;
  const latest = detail.events?.[0];

  return (
    <section className="briefing" aria-labelledby="briefing-heading">
      <h2 id="briefing-heading">Operational briefing</h2>
      <div className="briefing-grid">
        <article className={`brief brief-${truth.tone}`}>
          <span className="brief-label">Source truth</span>
          <strong>{truth.title}</strong>
          <p>{truth.detail}</p>
        </article>
        <article className={`brief ${dependents.length > 0 ? "brief-caution" : "brief-calm"}`}>
          <span className="brief-label">Blast radius</span>
          <strong>{plural(dependents.length, "dependent")}</strong>
          <p>
            {dependents.length === 0
              ? "Nothing in the catalog relies on this."
              : `${plural(direct, "direct dependent")}; ${plural(dependents.length - direct, "indirect dependent")}.`}
          </p>
        </article>
        <article className={`brief ${eventTone(latest)}`}>
          <span className="brief-label">Last operation</span>
          <strong>{latest ? `${latest.action} · ${latest.status}` : "No recorded actions"}</strong>
          <p>{latest ? eventSummary(latest) : "Dusk has no action receipt for this entity."}</p>
        </article>
      </div>

      {dependents.length > 0 && (
        <div className="impact">
          <h3>If this goes down</h3>
          <Rows
            items={dependents.map((dependent) => ({
              key: dependent.Ref,
              title: dependent.Ref,
              mono: true,
              tag: dependent.Depth === 1 ? "direct" : `${dependent.Depth} hops`,
              tagKind: dependent.Depth === 1 ? undefined : ("rel" as const),
              onOpen: () => onOpen(dependent.Ref),
            }))}
            empty=""
          />
        </div>
      )}
    </section>
  );
}

function sourceTruth(sources: EntitySource[]): {
  title: string;
  detail: string;
  tone: "calm" | "caution" | "observed";
} {
  const declared = sources.some((source) => !source.Observed);
  const observed = sources.some((source) => source.Observed);
  if (declared && observed) {
    return {
      title: "Declared + observed",
      detail: "Git declares this and a live source also sees it.",
      tone: "calm",
    };
  }
  if (observed) {
    return {
      title: "Observed only",
      detail: "A plugin sees this, but no repository declares it yet.",
      tone: "observed",
    };
  }
  return {
    title: declared ? "Declared only" : "Source unknown",
    detail: declared
      ? "Git declares this; no live observation is attached. It may simply be unwatched."
      : "Dusk cannot explain where this entity came from.",
    tone: "caution",
  };
}

function eventTone(event?: Event): "brief-calm" | "brief-caution" {
  return event?.status === "failed" || event?.status === "denied"
    ? "brief-caution"
    : "brief-calm";
}

function eventSummary(event: Event): string {
  const at = event.finished_at ?? event.started_at;
  const when = at ? new Date(at).toLocaleString() : "time unknown";
  return `${event.actor || "agent"} · ${when}${event.message ? ` · ${event.message}` : ""}`;
}

function plural(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? "" : "s"}`;
}

// url is the single most useful thing on the page for an operator, so it is
// promoted out of the attribute list into the header.
function url(attributes: Record<string, unknown> | undefined): string | undefined {
  const value = String(attributes?.url ?? "");
  return value.startsWith("http") ? value : undefined;
}

function shortVersion(version: string): string {
  return /^[0-9a-f]{8,}$/i.test(version) ? version.slice(0, 7) : version;
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
