import { useState } from "react";

import { api, type Drift, type Problem, type ResolvedBlock } from "./api";
import { Block } from "./Block";
import { Markdown } from "./Markdown";
import { Rows } from "./Rows";

// renderBlock turns one resolved query into a panel. The server decided what
// the block asked (ADR-0035); this only decides how the answer looks.
export function renderBlock(block: ResolvedBlock, onOpen: (ref: string) => void) {
  if (block.error) {
    return (
      <Block title={block.title} key={block.title}>
        <p className="quiet">
          This block could not be filled: {block.error}
        </p>
      </Block>
    );
  }

  switch (block.type) {
    case "kinds":
      return null;

    case "entities":
      return (
        <Block title={block.title} wide={block.wide} key={block.title}>
          <Rows
            items={(block.entities ?? []).map((e) => ({
              key: e.ref,
              title: e.title || e.name,
              sub: e.ref,
              mono: true,
              tag: e.kind,
              onOpen: () => onOpen(e.ref),
            }))}
            empty="Nothing matches this block's query."
          />
          <Truncated block={block} />
        </Block>
      );

    case "recent-notes":
      return (
        <Block title={block.title} wide={block.wide} key={block.title}>
          {(block.notes ?? []).length === 0 ? (
            <p className="quiet">
              Nothing written down yet. Notes are what an agent records when it works
              something out, and they show up here.
            </p>
          ) : (
            (block.notes ?? []).map((note) => (
              <article className="note note-compact" key={note.id}>
                <span className="tag note-kind">{note.kind}</span>
                <Markdown>{opening(note.body)}</Markdown>
              </article>
            ))
          )}
        </Block>
      );

    case "drift":
      return (
        <Block title={block.title} wide={block.wide} key={block.title}>
          <DriftList drift={block.drift ?? []} onOpen={onOpen} />
          <Truncated block={block} />
        </Block>
      );

    case "integrity":
      return (
        <Block title={block.title} wide={block.wide} key={block.title}>
          <ProblemList problems={block.problems ?? []} />
        </Block>
      );

    case "reads":
      return (
        <Block title={block.title} wide={block.wide} key={block.title}>
          {(block.reads ?? []).length === 0 ? (
            <p className="quiet">Nothing has been read yet.</p>
          ) : (
            <ul className="reads">
              {(block.reads ?? []).map((read) => (
                <li key={read.repository}>
                  <span className="reads-name ref">
                    {/* An observed source occupies the repository slot but is
                        not one, and should never read as something to clone. */}
                    {read.observed ? seenBy(read.repository) : read.repository}
                    {read.observed && <span className="seen">observed</span>}
                  </span>
                  <span className={read.error ? "reads-state bad" : "reads-state"}>
                    {read.error
                      ? "failed"
                      : `${read.entities} ${read.entities === 1 ? "entity" : "entities"}`}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Block>
      );
  }
}

function Truncated({ block }: { block: ResolvedBlock }) {
  if (!block.truncated) {
    return null;
  }
  // A silently shortened list reads as a complete one, which is worse than
  // showing fewer rows.
  return <p className="quiet more">Showing the first few. There are more.</p>;
}

function DriftList({
  drift,
  onOpen,
}: {
  drift: Drift[];
  onOpen: (ref: string) => void;
}) {
  if (drift.length === 0) {
    return (
      <p className="quiet">
        Nothing has drifted. Everything declared is running, and everything running is
        declared.
      </p>
    );
  }

  return (
    <div className="rows">
      {drift.map((item) => (
        <button
          key={item.Kind + item.Ref}
          type="button"
          className="row"
          onClick={() => onOpen(item.Ref)}
        >
          <span className="row-main">
            <span className="row-title">{item.Title || item.Ref}</span>
            <span className="row-sub ref">{item.Ref}</span>
          </span>
          <span
            className={item.Kind === "declared_not_observed" ? "tag gone" : "tag unknown"}
          >
            {item.Kind === "declared_not_observed" ? "not found" : "undeclared"}
          </span>
        </button>
      ))}
    </div>
  );
}

function ProblemList({ problems }: { problems: Problem[] }) {
  if (problems.length === 0) {
    return <p className="quiet">The catalog is sound.</p>;
  }

  return (
    <ul className="problems">
      {problems.map((problem) => (
        <li key={problem.Kind + problem.Ref}>
          <span className="ref">{problem.Ref}</span>
          <p className="quiet">{problem.Detail}.</p>
          {problem.Where.length > 0 && (
            <p className="ref where">{problem.Where.join(" · ")}</p>
          )}
          {problem.Kind === "orphaned_observations" && <Forget scope={problem.Ref} />}
        </li>
      ))}
    </ul>
  );
}

// Forget clears observations nothing will refresh. It is the one deletion Dusk
// offers, so it is a button somebody presses rather than something that happens
// on its own.
function Forget({ scope }: { scope: string }) {
  const [state, setState] = useState<"idle" | "working" | "done">("idle");
  const [problem, setProblem] = useState<string>();

  if (state === "done") {
    return <p className="quiet">Forgotten. It will disappear on the next read.</p>;
  }

  return (
    <>
      <button
        type="button"
        className="btn secondary"
        disabled={state === "working"}
        onClick={async () => {
          setState("working");
          setProblem(undefined);
          try {
            await api.forget(scope);
            setState("done");
          } catch (error) {
            setProblem(error instanceof Error ? error.message : String(error));
            setState("idle");
          }
        }}
      >
        {state === "working" ? "Forgetting" : "Forget these"}
      </button>
      {problem && (
        <p className="hint err" role="alert">
          {problem}
        </p>
      )}
    </>
  );
}

// seenBy strips the reserved prefix that puts an ingester in the repository
// slot, leaving the name of the thing that did the looking.
function seenBy(scope: string): string {
  return scope.replace(/^ingester:/, "");
}

// opening keeps a note preview to its first paragraph. The whole note is on
// the entity it attaches to, and a wall of prose here buries the other blocks.
function opening(body: string): string {
  const first = body.trim().split("\n\n")[0] ?? "";
  return first.length > 240 ? `${first.slice(0, 240).trimEnd()}…` : first;
}
