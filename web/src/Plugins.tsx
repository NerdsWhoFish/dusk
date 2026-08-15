import { useCallback, useEffect, useState } from "react";

import {
  api,
  type PluginHealth,
  type PluginOffer,
  type PluginProcess,
} from "./api";
import { Capabilities, Contributions, Output } from "./Capabilities";
import { Events } from "./Events";
import { ConfigForm } from "./PluginConfig";

const doing = {
  install: api.install,
  uninstall: api.uninstall,
  restart: api.restartPlugin,
};

type Doing = keyof typeof doing;

// Plugins is the marketplace: what the trusted orgs publish, what is installed
// here, and what has an update waiting. Installing runs somebody else's binary
// with Dusk's permissions, so the page says so rather than implying a store.
export function Plugins() {
  const [offers, setOffers] = useState<PluginOffer[]>([]);
  const [problem, setProblem] = useState<string>();
  const [loading, setLoading] = useState(true);

  // busy is per plugin, so installing one does not disable the others.
  const [busy, setBusy] = useState<Record<string, string>>({});

  const load = useCallback(async () => {
    try {
      const answer = await api.plugins();
      setOffers(answer.plugins ?? []);
      setProblem(answer.problem);
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // The listing is cached for a day, so this is how somebody asks GitHub now
  // rather than waiting to be told there is an update.
  const check = async (id: string) => {
    setBusy((current) => ({ ...current, [id]: "check" }));
    setProblem(undefined);
    try {
      const answer = await api.refreshPlugins();
      setOffers(answer.plugins ?? []);
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy((current) => {
        const next = { ...current };
        delete next[id];
        return next;
      });
    }
  };

  const act = async (id: string, what: Doing) => {
    setBusy((current) => ({ ...current, [id]: what }));
    setProblem(undefined);
    try {
      await doing[what](id);
      await load();
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy((current) => {
        const next = { ...current };
        delete next[id];
        return next;
      });
    }
  };

  if (loading) {
    return <p className="quiet">Looking for plugins.</p>;
  }

  return (
    <section className="block block-wide">
      <header className="block-head">
        <h2>Plugins</h2>
      </header>

      <p className="quiet plugins-note">
        A plugin runs as a subprocess of Dusk, with Dusk's permissions.
        Installing one is trusting whoever publishes it.
      </p>

      {problem && (
        <p className="hint err" role="alert">
          {problem}
        </p>
      )}

      {offers.length === 0 && !problem && (
        <p className="quiet">
          No plugins offered. Set <code>DUSK_PLUGIN_ORGS</code> to the GitHub
          organisations you trust.
        </p>
      )}

      <div className="rows">
        {offers.map((offer) => (
          <Offer
            key={offer.repository}
            offer={offer}
            busy={busy[offer.id]}
            onAct={act}
            onCheck={check}
            onSaved={load}
          />
        ))}
      </div>

      <h2>Recently run</h2>
      <Events />
    </section>
  );
}

function Offer({
  offer,
  busy,
  onAct,
  onCheck,
  onSaved,
}: {
  offer: PluginOffer;
  busy?: string;
  onAct: (id: string, what: Doing) => void;
  onCheck: (id: string) => void;
  onSaved: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [adding, setAdding] = useState("");
  const instances = Object.keys(offer.instances ?? {});

  return (
    <div className="row plugin">
      <div className="row-main">
        <span className="row-title">{offer.id}</span>
        <span className="row-sub">{offer.description || offer.repository}</span>
        <span className="plugin-meta">
          {offer.installed ? (
            <>
              <span className="tag rel">
                {offer.installed_version} installed
              </span>
              {!offer.running ? (
                <span className="tag gone">{phaseWord(offer.process)}</span>
              ) : failing(offer.health) ? (
                <span className="tag gone">failing</span>
              ) : (
                <span className="tag">running</span>
              )}
              {(offer.process?.restarts ?? 0) > 0 && (
                <span className="tag unknown">
                  restarted {offer.process?.restarts}&times;
                </span>
              )}
              {offer.update_available && (
                <span className="tag unknown">{offer.version} available</span>
              )}
            </>
          ) : (
            <span className="tag">{offer.version || "no release"}</span>
          )}
        </span>
      </div>

      <ProcessNote process={offer.process} />

      {(offer.health ?? []).some((h) => h.problem) && (
        <div className="plugin-health">
          {(offer.health ?? [])
            .filter((h) => h.problem)
            .map((h) => (
              <p className="hint err" role="alert" key={h.instance ?? ""}>
                {h.instance ? `${h.instance}: ` : ""}
                {h.problem}
              </p>
            ))}
        </div>
      )}

      <div className="plugin-actions">
        {offer.installed && offer.running && (
          <button
            type="button"
            className="btn secondary"
            onClick={() => setOpen((was) => !was)}
          >
            {open ? "Close" : "Configure"}
          </button>
        )}
        {offer.installed && !offer.running && (
          <button
            type="button"
            className="btn"
            disabled={Boolean(busy)}
            onClick={() => onAct(offer.id, "restart")}
          >
            {busy === "restart" ? "Starting" : "Start"}
          </button>
        )}
        {offer.installed && (
          <button
            type="button"
            className="btn secondary"
            disabled={Boolean(busy)}
            onClick={() => onCheck(offer.id)}
          >
            {busy === "check" ? "Checking" : "Check for updates"}
          </button>
        )}
        {offer.installed && offer.update_available && (
          <button
            type="button"
            className="btn"
            disabled={Boolean(busy)}
            onClick={() => onAct(offer.id, "install")}
          >
            {busy === "install" ? "Updating" : "Update"}
          </button>
        )}
        {offer.installed ? (
          <button
            type="button"
            className="btn secondary"
            disabled={Boolean(busy)}
            onClick={() => onAct(offer.id, "uninstall")}
          >
            {busy === "uninstall" ? "Removing" : "Uninstall"}
          </button>
        ) : (
          <button
            type="button"
            className="btn"
            disabled={Boolean(busy) || !offer.version}
            onClick={() => onAct(offer.id, "install")}
          >
            {busy === "install" ? "Installing" : "Install"}
          </button>
        )}
      </div>

      {open && (
        <div className="plugin-panel">
          <Contributions offer={offer} />
          <Capabilities offer={offer} onChanged={onSaved} />
          <ConfigForm offer={offer} onSaved={onSaved} />

          {instances.map((name) => (
            <div key={name} className="plugin-instance">
              <h3>{name}</h3>
              <ConfigForm offer={offer} instance={name} onSaved={onSaved} />
            </div>
          ))}

          {/* A second source is a second instance of the same plugin, not a
              second install: one Kubernetes plugin observes one cluster. */}
          <div className="plugin-instance">
            <label className="plugin-field" htmlFor="new-instance">
              <span className="plugin-field-label">Add another source</span>
              <input
                id="new-instance"
                value={adding}
                placeholder="a name for it, such as another cluster"
                onChange={(e) => setAdding(e.target.value)}
              />
            </label>
            {adding.trim() && (
              <ConfigForm
                offer={offer}
                instance={adding.trim()}
                onSaved={() => {
                  setAdding("");
                  onSaved();
                }}
              />
            )}
          </div>

          <div className="plugin-instance">
            <h3>Output</h3>
            <Output id={offer.id} />
          </div>
        </div>
      )}
    </div>
  );
}

// failing reports whether any of a plugin's configurations last failed. A
// plugin can be running and observing nothing, and "running" alone said so
// only in the pod log.
function failing(health?: PluginHealth[]): boolean {
  return (health ?? []).some((entry) => Boolean(entry.problem));
}

// phaseWord distinguishes a plugin on its way back from one nothing is trying
// to bring back, which decide different things for whoever is reading.
function phaseWord(process?: PluginProcess): string {
  switch (process?.phase) {
    case "restarting":
      return "restarting";
    case "failed":
      return "will not start";
    default:
      return "not running";
  }
}

// when renders a timestamp, or nothing at all for the zero time Go sends when
// there is nothing to time. Rendering that gives the year 1.
function when(value: string, prefix: string): string {
  const at = new Date(value);
  if (!value || Number.isNaN(at.getTime()) || at.getUTCFullYear() < 2000) {
    return "";
  }
  return prefix + at.toLocaleString();
}

// ProcessNote says how a plugin's process is doing when that is not simply
// "it has been up the whole time".
function ProcessNote({ process }: { process?: PluginProcess }) {
  if (!process || (process.phase === "running" && process.restarts === 0)) {
    return null;
  }

  return (
    <div className="plugin-health">
      {process.phase === "failed" && (
        <p className="hint err" role="alert">
          It would not stay up after {process.attempts} attempts, so nothing is
          restarting it. {process.exit}
        </p>
      )}
      {process.phase === "restarting" && (
        <p className="hint err" role="alert">
          Its process went away and Dusk is starting it again. {process.exit}
        </p>
      )}
      {process.phase === "running" && process.restarts > 0 && (
        <p className="quiet">
          Restarted {process.restarts === 1 ? "once" : `${process.restarts} times`}
          {when(process.since, ", up since ")}.
          {process.exit ? ` Last exit: ${process.exit}` : ""}
        </p>
      )}
      {process.phase === "stopped" && <p className="quiet">Dusk stopped it.</p>}
    </div>
  );
}
