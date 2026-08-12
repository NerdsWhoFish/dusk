import { useEffect, useState } from "react";
import { api } from "./api";
import type { Entity, Overview, SearchResult } from "./api";
import { handle } from "./App";
import { Block } from "./Block";
import { Markdown } from "./Markdown";
import { Rows } from "./Rows";

export function Landing({ onOpen }: { onOpen: (ref: string) => void }) {
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState<string | null>(null);
  const [overview, setOverview] = useState<Overview | null>(null);
  const [results, setResults] = useState<SearchResult[] | null>(null);
  const [entities, setEntities] = useState<Entity[] | null>(null);
  const [problem, setProblem] = useState<string | null>(null);

  useEffect(() => {
    api.overview().then(setOverview).catch(handle(setProblem));
  }, []);

  useEffect(() => {
    const term = query.trim();
    if (!term) {
      setResults(null);
      return;
    }

    let live = true;
    const timer = setTimeout(() => {
      api
        .search(term)
        .then((data) => live && setResults(data.results))
        .catch(handle(setProblem));
    }, 140);

    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [query]);

  useEffect(() => {
    if (!kind) {
      setEntities(null);
      return;
    }
    let live = true;
    api
      .entities(kind)
      .then((data) => live && setEntities(data.entities))
      .catch(handle(setProblem));
    return () => {
      live = false;
    };
  }, [kind]);

  const searching = query.trim().length > 0;

  return (
    <>
      <div className="hero">
        <label className="visually-hidden" htmlFor="q">
          Search the catalog
        </label>
        <input
          id="q"
          type="search"
          value={query}
          autoFocus
          spellCheck={false}
          autoComplete="off"
          placeholder="A service, a host, something you half remember"
          onChange={(e) => setQuery(e.target.value)}
        />
        {overview && !searching && overview.total > 0 && (
          <p className="hero-sub">
            {plural(overview.total, "thing")} across{" "}
            {plural(overview.declaring, "repository", "repositories")}
          </p>
        )}
      </div>

      {problem && <p className="problem">{problem}</p>}

      {searching ? (
        <Rows
          items={(results ?? []).map((r) => ({
            key: r.Ref,
            title: r.Title || r.Ref,
            sub: r.Snippet || r.Ref,
            tag: r.Kind,
            onOpen: () => onOpen(r.Ref),
          }))}
          empty={`Nothing matches "${query.trim()}". The catalog only knows what a repository wrote down.`}
        />
      ) : (
        <Portal
          overview={overview}
          kind={kind}
          entities={entities}
          onKind={setKind}
          onOpen={onOpen}
        />
      )}
    </>
  );
}

function Portal({
  overview,
  kind,
  entities,
  onKind,
  onOpen,
}: {
  overview: Overview | null;
  kind: string | null;
  entities: Entity[] | null;
  onKind: (kind: string | null) => void;
  onOpen: (ref: string) => void;
}) {
  if (!overview) {
    return (
      <div className="blocks" aria-hidden="true">
        <div className="skeleton tall" />
        <div className="skeleton tall" />
      </div>
    );
  }

  if (overview.total === 0) {
    return (
      <p className="empty">
        Nothing here yet. Add a <code>dusk.md</code> to a repository Dusk can see and it
        appears on the next reconcile.
      </p>
    );
  }

  return (
    <>
      {/* The shape of the estate, and the fastest way in for somebody who
          does not yet know what they are looking for. */}
      <div className="kinds">
        {overview.kinds.map((entry) => (
          <button
            key={entry.Kind}
            type="button"
            className={`chip kind-${entry.Kind}${kind === entry.Kind ? " on" : ""}`}
            aria-pressed={kind === entry.Kind}
            onClick={() => onKind(kind === entry.Kind ? null : entry.Kind)}
          >
            <span className="chip-count">{entry.Count}</span>
            {entry.Count === 1 ? entry.Kind : pluralize(entry.Kind)}
          </button>
        ))}
      </div>

      {kind && (
        <Rows
          items={(entities ?? []).map((e) => ({
            key: e.ref,
            title: e.title || e.name,
            sub: e.ref,
            mono: true,
            tag: e.kind,
            onOpen: () => onOpen(e.ref),
          }))}
          empty="Nothing of that kind."
        />
      )}

      {!kind && (
        <div className="blocks">
          <Block title="Recent notes" wide>
            {overview.notes.length === 0 ? (
              <p className="quiet">
                Nothing written down yet. Notes are what an agent records when it works
                something out, and they show up here.
              </p>
            ) : (
              overview.notes.map((note) => (
                <article className="note note-compact" key={note.id}>
                  <span className="tag note-kind">{note.kind}</span>
                  <Markdown>{clamp(note.body)}</Markdown>
                </article>
              ))
            )}
          </Block>

          <Block title="What Dusk has read">
            {overview.repositories.length === 0 ? (
              <p className="quiet">No repository has been read yet.</p>
            ) : (
              <ul className="reads">
                {overview.repositories.map((repo) => (
                  <li key={repo.Repository + repo.GitRef}>
                    <span className="reads-name ref">{repo.Repository}</span>
                    <span className={repo.Error ? "reads-state bad" : "reads-state"}>
                      {repo.Error
                        ? "failed"
                        : `${repo.Entities} ${repo.Entities === 1 ? "entity" : "entities"}`}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </Block>
        </div>
      )}
    </>
  );
}

// pluralize handles the kinds a catalog actually uses. Kind is a free string
// (ADR-0007), so this degrades to adding an s rather than pretending to know
// English: "repositorys" on the landing page is the failure it exists to stop.
function pluralize(word: string): string {
  if (/(s|x|z|ch|sh)$/.test(word)) {
    return `${word}es`;
  }
  if (/[^aeiou]y$/.test(word)) {
    return `${word.slice(0, -1)}ies`;
  }
  return `${word}s`;
}

function plural(count: number, one: string, many?: string): string {
  return `${count} ${count === 1 ? one : (many ?? pluralize(one))}`;
}

// clamp keeps a note preview to its opening. The full note is on the entity it
// attaches to, and a wall of prose here would bury the other blocks.
function clamp(body: string): string {
  const firstParagraph = body.trim().split("\n\n")[0] ?? "";
  return firstParagraph.length > 240
    ? `${firstParagraph.slice(0, 240).trimEnd()}…`
    : firstParagraph;
}
