import { useCallback, useEffect, useState } from "react";
import { api } from "./api";
import type { Entity, Home, SearchResult } from "./api";
import { handle } from "./App";
import { CatalogCheckpoint } from "./CatalogCheckpoint";
import { renderBlock } from "./blocks";
import { Markdown } from "./Markdown";
import { Rows } from "./Rows";
import { group, useVocabulary } from "./vocabulary";
import type { KindGroup } from "./vocabulary";

export function Landing({
  onOpen,
  onOpenNote,
}: {
  onOpen: (ref: string) => void;
  onOpenNote: (id: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState<string | null>(null);
  const [home, setHome] = useState<Home | null>(null);
  const [results, setResults] = useState<SearchResult[] | null>(null);
  const [entities, setEntities] = useState<Entity[] | null>(null);
  const [problem, setProblem] = useState<string | null>(null);

  // Reloading is what closing a note from a block needs: the page carried the
  // proof token, and writing invalidates it along with what it described.
  const load = useCallback(() => {
    api
      .home({ onFresh: setHome, onError: handle(setProblem) })
      .then(setHome)
      .catch(handle(setProblem));
  }, []);

  useEffect(() => {
    load();
  }, [load]);

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
  const kinds =
    home?.blocks.find((block) => block.type === "kinds")?.kinds ?? [];
  const total = kinds.reduce((sum, entry) => sum + entry.Count, 0);
  const vocabulary = useVocabulary();
  const groups = group(kinds, vocabulary);

  // Absent means yes: a page that says nothing about search still gets it.
  const searchable = home?.search !== false;

  return (
    <>
      {searchable && (
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
          {home && !searching && total > 0 && (
            <p className="hero-sub">{plural(total, "thing")} in the catalog</p>
          )}
        </div>
      )}

      {problem && <p className="problem">{problem}</p>}
      {home?.problem && (
        <p className="problem">
          The declared home page could not be used, so this is the default one.{" "}
          {home.problem}
        </p>
      )}

      {searching ? (
        <Rows
          items={(results ?? []).map((r) => ({
            key: r.Ref,
            title: r.Title || r.Ref,
            sub: r.Snippet || r.Ref,
            tag: r.Kind,
            onIntent: () => {
              const preload =
                r.Type === "note" ? api.prefetchNote(r.Ref) : api.prefetchEntity(r.Ref);
              void preload.catch(() => undefined);
            },
            onOpen: () => (r.Type === "note" ? onOpenNote(r.Ref) : onOpen(r.Ref)),
          }))}
          empty={`Nothing matches "${query.trim()}". The catalog only knows what a repository wrote down.`}
        />
      ) : (
        <Portal
          home={home}
          groups={groups}
          kind={kind}
          entities={entities}
          onKind={setKind}
          onOpen={onOpen}
          onChanged={load}
        />
      )}
    </>
  );
}

function Portal({
  home,
  groups,
  kind,
  entities,
  onKind,
  onOpen,
  onChanged,
}: {
  home: Home | null;
  groups: KindGroup[];
  kind: string | null;
  entities: Entity[] | null;
  onKind: (kind: string | null) => void;
  onOpen: (ref: string) => void;
  onChanged: () => void;
}) {
  if (!home) {
    return (
      <div className="blocks" aria-hidden="true">
        <div className="skeleton tall" />
        <div className="skeleton tall" />
      </div>
    );
  }

  if (groups.length === 0 && home.blocks.every((block) => empty(block))) {
    return (
      <p className="empty">
        Nothing here yet. Add a <code>dusk.md</code> to a repository Dusk can
        see, or install a plugin to observe something, and it appears on the
        next pass.
      </p>
    );
  }

  return (
    <>
      <CatalogCheckpoint repositories={home.repositories ?? []} />

      {groups.length > 0 && (
        <div className="kinds">
          {groups.map((entry) => (
            <div className="kinds-group" key={entry.role || "all"}>
              {/* Labelled only when there is more than one, so an operator who
                  has minted nothing sees the row they always saw. */}
              {entry.role && <span className="kinds-role">{entry.role}</span>}
              {entry.chips.map((one) => (
                <button
                  key={one.kind}
                  type="button"
                  className={`chip kind-${one.kind}${kind === one.kind ? " on" : ""}`}
                  aria-pressed={kind === one.kind}
                  onClick={() => onKind(kind === one.kind ? null : one.kind)}
                >
                  <span className="chip-count">{one.count}</span>
                  {one.count === 1 ? one.kind : pluralize(one.kind)}
                  {/* Both halves, because two chips for one kind is the split
                      worth seeing and only an alias makes it legible. */}
                  {one.aliasOf ? (
                    <span className="chip-alias">spelling of {one.aliasOf}</span>
                  ) : (
                    one.aliases.length > 0 && (
                      <span className="chip-alias">also {one.aliases.join(", ")}</span>
                    )
                  )}
                </button>
              ))}
            </div>
          ))}
        </div>
      )}

      {kind ? (
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
      ) : (
        <>
          {home.prose && <Markdown>{home.prose}</Markdown>}
          <div className="blocks">
            {home.blocks.map((block) => renderBlock(block, onOpen, home.proof, onChanged))}
          </div>
        </>
      )}
    </>
  );
}

function empty(block: {
  entities?: unknown[];
  notes?: unknown[];
  drift?: unknown[];
  problems?: unknown[];
  reads?: unknown[];
}): boolean {
  return (
    (block.entities?.length ?? 0) === 0 &&
    (block.notes?.length ?? 0) === 0 &&
    (block.drift?.length ?? 0) === 0 &&
    (block.problems?.length ?? 0) === 0 &&
    (block.reads?.length ?? 0) === 0
  );
}

// pluralize handles the kinds a catalog actually uses. Kind is a free string
// (ADR-0007), so this degrades to adding an s rather than pretending to know
// English: "repositorys" on the landing page is the failure it exists to stop.
export function pluralize(word: string): string {
  if (/(s|x|z|ch|sh)$/.test(word)) {
    return `${word}es`;
  }
  if (/[^aeiou]y$/.test(word)) {
    return `${word.slice(0, -1)}ies`;
  }
  return `${word}s`;
}

export function plural(count: number, one: string, many?: string): string {
  return `${count} ${count === 1 ? one : (many ?? pluralize(one))}`;
}
