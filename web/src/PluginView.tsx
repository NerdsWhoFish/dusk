import { createElement, useEffect, useState } from "react";

import type { Entity, PluginView as View } from "./api";
import { DeclaredView } from "./DeclaredView";

// loaded keeps a module from being imported twice. Defining a custom element
// that already exists throws, and React mounting the same view on two pages
// would otherwise do exactly that.
const loaded = new Set<string>();

// PluginBlock renders one contribution. A declared view is drawn here and needs
// nothing from the plugin at render time; an element is the plugin's own
// JavaScript, which shares no runtime with this app (ADR-0020).
export function PluginBlock({
  view,
  entityRef,
  entities,
  proof,
  onOpen,
}: {
  view: View;
  entityRef?: string;
  entities?: Entity[];
  proof?: string;
  onOpen?: (ref: string) => void;
}) {
  return (
    <section className="block">
      <header className="block-head">
        <h2>{view.title || view.plugin}</h2>
      </header>

      {view.spec ? (
        <DeclaredView spec={view.spec} entities={entities ?? []} onOpen={onOpen} />
      ) : (
        <Drawn view={view} entityRef={entityRef} entities={entities} proof={proof} />
      )}
    </section>
  );
}

function Drawn({
  view,
  entityRef,
  entities,
  proof,
}: {
  view: View;
  entityRef?: string;
  entities?: unknown[];
  proof?: string;
}) {
  const source = view.source ?? "";
  const [problem, setProblem] = useState<string>();
  const [ready, setReady] = useState(loaded.has(source));

  useEffect(() => {
    if (!source || loaded.has(source)) {
      setReady(Boolean(source));
      return;
    }

    loaded.add(source);
    import(/* @vite-ignore */ source)
      .then(() => setReady(true))
      .catch((error: unknown) => {
        loaded.delete(source);
        setProblem(error instanceof Error ? error.message : String(error));
      });
  }, [source]);

  if (problem) {
    return (
      <p className="hint err" role="alert">
        {view.plugin} could not be loaded: {problem}
      </p>
    );
  }
  if (!ready || !view.element) {
    return <p className="quiet">Loading {view.plugin}.</p>;
  }

  // createElement rather than JSX: the tag is only known at runtime, and JSX
  // would treat it as a component. React 19 assigns a non-string value as a
  // property, which is how a result set reaches the element unstringified.
  return createElement(view.element, { "entity-ref": entityRef, entities, proof });
}
