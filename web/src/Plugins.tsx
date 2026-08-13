import { useCallback, useEffect, useState } from "react";

import { api, type PluginOffer } from "./api";

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

  const act = async (id: string, what: "install" | "uninstall") => {
    setBusy((current) => ({ ...current, [id]: what }));
    setProblem(undefined);
    try {
      await (what === "install" ? api.install(id) : api.uninstall(id));
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
        A plugin runs as a subprocess of Dusk, with Dusk's permissions. Installing
        one is trusting whoever publishes it.
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
          />
        ))}
      </div>
    </section>
  );
}

function Offer({
  offer,
  busy,
  onAct,
}: {
  offer: PluginOffer;
  busy?: string;
  onAct: (id: string, what: "install" | "uninstall") => void;
}) {
  return (
    <div className="row plugin">
      <div className="row-main">
        <span className="row-title">{offer.id}</span>
        <span className="row-sub">{offer.description || offer.repository}</span>
        <span className="plugin-meta">
          {offer.installed ? (
            <>
              <span className="tag rel">{offer.installed_version} installed</span>
              {offer.running ? (
                <span className="tag">running</span>
              ) : (
                <span className="tag gone">not running</span>
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

      <div className="plugin-actions">
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
    </div>
  );
}
