import { useCallback, useEffect, useState } from "react";
import { api } from "./api";
import type { AIAnswer, AIConfig, Entity, Home, SearchResult } from "./api";
import { handle } from "./App";
import { CatalogCheckpoint } from "./CatalogCheckpoint";
import { renderBlock } from "./blocks";
import { Markdown } from "./Markdown";
import { Rows } from "./Rows";
import { group, useVocabulary } from "./vocabulary";
import type { KindGroup } from "./vocabulary";

const aiDefaultKey = "dusk:ai:default-model";
type SearchMode = "search" | "ask";
type LandingHistory = {
  scope: string;
  mode: SearchMode;
  query: string;
  model: string;
  answer: AIAnswer | null;
};

export function Landing({
  cacheScope,
  onOpen,
  onOpenNote,
}: {
  cacheScope: string;
  onOpen: (ref: string) => void;
  onOpenNote: (id: string) => void;
}) {
  const [restored] = useState(() => restoredLanding(cacheScope));
  const [query, setQuery] = useState(restored?.query ?? "");
  const [kind, setKind] = useState<string | null>(null);
  const [home, setHome] = useState<Home | null>(null);
  const [results, setResults] = useState<SearchResult[] | null>(null);
  const [entities, setEntities] = useState<Entity[] | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [mode, setMode] = useState<SearchMode>(restored?.mode ?? "search");
  const [ai, setAI] = useState<AIConfig | null>(null);
  const [model, setModel] = useState(restored?.model ?? "");
  const [defaultModel, setDefaultModel] = useState("");
  const [answer, setAnswer] = useState<AIAnswer | null>(restored?.answer ?? null);
  const [asking, setAsking] = useState(false);
  const [aiProblem, setAIProblem] = useState<string | null>(null);

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
    api
      .ai()
      .then((config) => {
        setAI(config);
        if (!config.enabled) {
          setMode("search");
          setAnswer(null);
          return;
        }
        const stored = storedDefaultModel();
        const deploymentDefault = stored && config.models.includes(stored)
          ? stored
          : (config.default_model ?? config.models[0] ?? "");
        const selected = restored?.model && config.models.includes(restored.model)
          ? restored.model
          : deploymentDefault;
        setModel(selected);
        setDefaultModel(deploymentDefault);
      })
      .catch(handle(setProblem));
  }, []);

  useEffect(() => {
    const term = query.trim();
    if (!term || mode !== "search") {
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
  }, [query, mode]);

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

  const ask = () => {
    const question = query.trim();
    if (!question || !model || asking) return;
    setAsking(true);
    setAnswer(null);
    setAIProblem(null);
    api
      .ask(question, model)
      .then(setAnswer)
      .catch((error: unknown) => {
        if (error instanceof Error && error.message === "session expired") {
          handle(setAIProblem)(error);
          return;
        }
        setAIProblem(error instanceof Error ? error.message : String(error));
      })
      .finally(() => setAsking(false));
  };

  const rememberModel = () => {
    if (!model) return;
    try {
      localStorage.setItem(aiDefaultKey, model);
    } catch {
      // A blocked local store makes this tab the default's lifetime. The model
      // remains selected and asking the catalog still works.
    }
    setDefaultModel(model);
  };

  const preserveAnswer = () => {
    const current = typeof history.state === "object" && history.state !== null
      ? history.state
      : {};
    history.replaceState({
      ...current,
      duskLanding: { scope: cacheScope, mode, query, model, answer } satisfies LandingHistory,
    }, "", location.href);
  };

  const openAnswerEntity = (ref: string) => {
    preserveAnswer();
    onOpen(ref);
  };

  const openAnswerNote = (id: string) => {
    preserveAnswer();
    onOpenNote(id);
  };

  return (
    <>
      {searchable && (
        <div className="hero">
          {ai?.enabled && (
            <div className="search-modes" role="group" aria-label="Search mode">
              <button
                type="button"
                className={mode === "search" ? "on" : ""}
                aria-pressed={mode === "search"}
                onClick={() => setMode("search")}
              >
                Search
              </button>
              <button
                type="button"
                className={mode === "ask" ? "on" : ""}
                aria-pressed={mode === "ask"}
                onClick={() => setMode("ask")}
              >
                Ask AI
              </button>
            </div>
          )}
          <label className="visually-hidden" htmlFor="q">
            {mode === "ask" ? "Ask the catalog" : "Search the catalog"}
          </label>
          <form
            className={`search-form ${mode}`}
            onSubmit={(event) => {
              event.preventDefault();
              if (mode === "ask") ask();
            }}
          >
            <input
              id="q"
              type="search"
              value={query}
              autoFocus
              spellCheck={false}
              autoComplete="off"
              placeholder={
                mode === "ask"
                  ? "Where does Jellyfin run? What do we know about the NAS?"
                  : "A service, a host, something you half remember"
              }
              onChange={(event) => {
                setQuery(event.target.value);
                setAIProblem(null);
                setAnswer(null);
              }}
            />
            {mode === "ask" && (
              <button
                type="submit"
                className="ask-submit"
                disabled={!query.trim() || !model || asking}
              >
                {asking ? "Asking…" : "Ask"}
              </button>
            )}
          </form>
          {mode === "ask" && ai?.enabled && (
            <div className="ai-controls">
              <label>
                <span>Model</span>
                <select value={model} onChange={(event) => setModel(event.target.value)}>
                  {ai.models.map((name) => (
                    <option key={name} value={name}>{name}</option>
                  ))}
                </select>
              </label>
              <button
                type="button"
                className={model === defaultModel ? "model-default on" : "model-default"}
                disabled={!model || model === defaultModel}
                onClick={rememberModel}
              >
                {model === defaultModel ? "Default model" : "Make default"}
              </button>
              {ai.provider && (
                <span className="ai-provider">Relevant excerpts go to {ai.provider}</span>
              )}
            </div>
          )}
          {home && !searching && mode === "search" && total > 0 && (
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

      {mode === "ask" && searching ? (
        <AIResult
          answer={answer}
          asking={asking}
          problem={aiProblem}
          onOpen={openAnswerEntity}
          onOpenNote={openAnswerNote}
        />
      ) : searching ? (
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

function AIResult({
  answer,
  asking,
  problem,
  onOpen,
  onOpenNote,
}: {
  answer: AIAnswer | null;
  asking: boolean;
  problem: string | null;
  onOpen: (ref: string) => void;
  onOpenNote: (id: string) => void;
}) {
  if (problem) {
    return <p className="problem ai-problem">{problem}</p>;
  }
  if (asking) {
    return (
      <section className="ai-answer loading" aria-live="polite" aria-busy="true">
        <span className="ai-orbit" aria-hidden="true" />
        <div>
          <strong>Reading the estate</strong>
          <p>Finding the relevant entities, relations, and notes before asking the model.</p>
        </div>
      </section>
    );
  }
  if (!answer) {
    return (
      <p className="ai-empty">
        Ask a concrete question. Dusk retrieves a small visible slice of the catalog, then
        gives that evidence to the selected model.
      </p>
    );
  }
  return (
    <section className="ai-answer" aria-live="polite">
      <div className="ai-answer-head">
        <span>Answer from the estate</span>
        <code>{answer.model}</code>
      </div>
      <Markdown>{answer.answer}</Markdown>
      {answer.sources.length > 0 && (
        <div className="ai-sources">
          <span className="ai-sources-title">Catalog sources</span>
          <div className="ai-source-list">
            {answer.sources.map((source, index) => (
              <button
                key={`${source.type}:${source.ref}`}
                type="button"
                onClick={() => source.type === "note" ? onOpenNote(source.ref) : onOpen(source.ref)}
              >
                <span className="source-marker">S{index + 1}</span>
                <span className="source-title">{source.title || source.ref}</span>
                <span className="source-kind">{source.kind}</span>
              </button>
            ))}
          </div>
        </div>
      )}
      <p className="ai-caveat">Generated from catalog excerpts. Open the sources before acting.</p>
    </section>
  );
}

function storedDefaultModel(): string | null {
  try {
    return localStorage.getItem(aiDefaultKey);
  } catch {
    return null;
  }
}

function restoredLanding(cacheScope: string): LandingHistory | null {
  const state = history.state as { duskLanding?: LandingHistory } | null;
  const landing = state?.duskLanding;
  return landing?.scope === cacheScope ? landing : null;
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
