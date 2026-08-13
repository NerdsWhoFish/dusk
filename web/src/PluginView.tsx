import { createElement, useEffect, useState } from "react";

import type { PluginView as View } from "./api";

// loaded keeps a module from being imported twice. Defining a custom element
// that already exists throws, and React mounting the same view on two pages
// would otherwise do exactly that.
const loaded = new Set<string>();

// PluginBlock mounts a plugin's custom element. React renders the tag like any
// other, which is why ADR-0020 chose Web Components: the plugin brings its own
// rendering and shares no runtime with this app.
export function PluginBlock({
  view,
  entityRef,
  entities,
}: {
  view: View;
  entityRef: string;
  entities?: unknown[];
}) {
  const [problem, setProblem] = useState<string>();
  const [ready, setReady] = useState(loaded.has(view.source));

  useEffect(() => {
    if (loaded.has(view.source)) {
      setReady(true);
      return;
    }

    loaded.add(view.source);
    import(/* @vite-ignore */ view.source)
      .then(() => setReady(true))
      .catch((error: unknown) => {
        loaded.delete(view.source);
        setProblem(error instanceof Error ? error.message : String(error));
      });
  }, [view.source]);

  return (
    <section className="block">
      <header className="block-head">
        <h2>{view.title || view.plugin}</h2>
      </header>

      {problem ? (
        <p className="hint err" role="alert">
          {view.plugin} could not be loaded: {problem}
        </p>
      ) : ready ? (
        // createElement rather than JSX: the tag is a string only known at
        // runtime, and JSX would treat it as a component.
        // React 19 assigns non-string values to a custom element as
        // properties rather than attributes, which is how a resolved query
        // reaches the element without being stringified into markup.
        createElement(view.element, { "entity-ref": entityRef, entities })
      ) : (
        <p className="quiet">Loading {view.plugin}.</p>
      )}
    </section>
  );
}
