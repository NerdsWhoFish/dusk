import { useState } from "react";

import { Actions } from "./Actions";
import { api } from "./api";
import type { Action, OutputLine, PluginOffer } from "./api";
import { PluginBlock } from "./PluginView";

// Capabilities is where a plugin's actions are turned on. Declaring one does
// not grant it: installing a plugin must not silently hand over the ability to
// change things, so enabling is a deliberate act here (ADR-0015).
export function Capabilities({
  offer,
  onChanged,
}: {
  offer: PluginOffer;
  onChanged: () => void;
}) {
  const actions = offer.actions ?? [];
  if (actions.length === 0) {
    return null;
  }

  const standalone = actions.filter((action) => (action.kinds ?? []).length === 0);

  return (
    <div className="plugin-instance">
      <h3>Capabilities</h3>
      <p className="hint">
        Nothing here runs until it is turned on. What is enabled can be run by an
        agent as well as from this page.
      </p>

      {actions.map((action) => (
        <Toggle key={action.name} id={offer.id} action={action} onChanged={onChanged} />
      ))}

      {standalone.some((action) => action.enabled) && (
        <Actions actions={standalone} onRan={onChanged} />
      )}
    </div>
  );
}

function Toggle({
  id,
  action,
  onChanged,
}: {
  id: string;
  action: Action;
  onChanged: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string>();

  const flip = async () => {
    setBusy(true);
    setProblem(undefined);
    try {
      await api.enableAction(id, action.name, !action.enabled);
      onChanged();
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="capability">
      <label className="plugin-field check" htmlFor={`enable-${id}-${action.name}`}>
        <input
          id={`enable-${id}-${action.name}`}
          type="checkbox"
          checked={action.enabled}
          disabled={busy}
          onChange={flip}
        />
        <span>
          {action.name}
          <span className={`tag class-${action.class}`}>
            {action.class.replace(/_/g, " ")}
          </span>
        </span>
        <span className="hint">{action.description}</span>
      </label>

      {action.approval === "confirm" && action.enabled && (
        <p className="hint">Every run of this is confirmed separately.</p>
      )}
      {(action.then ?? []).length > 0 && (
        <p className="hint">May then run {(action.then ?? []).join(" and ")}.</p>
      )}
      {problem && (
        <p className="hint err" role="alert">
          {problem}
        </p>
      )}
    </div>
  );
}

// Contributions are what a plugin renders about itself rather than about one
// entity, which is where something it does not observe yet gets created.
export function Contributions({ offer }: { offer: PluginOffer }) {
  const views = offer.views ?? [];
  if (views.length === 0) {
    return null;
  }

  return (
    <>
      {views.map((view) => (
        <PluginBlock key={view.source ?? `${view.plugin}-${view.title}`} view={view} />
      ))}
    </>
  );
}

// Output is what the plugin printed. Its error string says roughly what went
// wrong; this is what it left out, without reading the pod running Dusk.
export function Output({ id }: { id: string }) {
  const [lines, setLines] = useState<OutputLine[]>();
  const [problem, setProblem] = useState<string>();

  if (lines === undefined) {
    return (
      <button
        type="button"
        className="btn secondary"
        onClick={() => {
          api
            .output(id)
            .then((answer) => setLines(answer.output ?? []))
            .catch((error: unknown) =>
              setProblem(error instanceof Error ? error.message : String(error)),
            );
        }}
      >
        {problem ?? "Show output"}
      </button>
    );
  }

  if (lines.length === 0) {
    return <p className="quiet">It has printed nothing.</p>;
  }

  return (
    <div className="scroller">
      <pre className="plugin-output">
        {lines.map((line) => `${marker(line.stream)}${line.text}`).join("\n")}
      </pre>
    </div>
  );
}

// marker separates what the plugin said from what Dusk said about it, which
// matters most where the two interleave: around a crash and its restart.
function marker(stream: string): string {
  switch (stream) {
    case "stderr":
      return "! ";
    case "dusk":
      return "= ";
    default:
      return "  ";
  }
}
