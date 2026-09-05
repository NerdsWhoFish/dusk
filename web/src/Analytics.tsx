import type { CSSProperties } from "react";

import type { AnalyticsSnapshot } from "./api";
import { Markdown } from "./Markdown";
import { opening } from "./Notes";

export function Analytics({
  snapshot,
  onOpenNote,
}: {
  snapshot: AnalyticsSnapshot;
  onOpenNote: (id: string) => void;
}) {
  const sources = snapshot.sources ?? [];
  const knowledge = snapshot.knowledge ?? [];
  const plugins = snapshot.plugins ?? [];
  const strongest = sources[0]?.entities ?? 1;

  return (
    <div className="analytics">
      <div className="analytics-lead">
        <div className="analytics-orbit" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
        <div className="analytics-total">
          <span>Current catalog</span>
          <strong>{number(snapshot.entities)}</strong>
          <p>
            things in the current estate · {number(snapshot.repositories)} declared {snapshot.repositories === 1 ? "source" : "sources"}
          </p>
        </div>
        <dl className="analytics-facts">
          <div>
            <dt>Knowledge</dt>
            <dd>{number(snapshot.notes)}</dd>
          </div>
          <div>
            <dt>Open work</dt>
            <dd>{number(snapshot.open_work)}</dd>
          </div>
          <div>
            <dt>Recent actions</dt>
            <dd>{number(snapshot.actions)}</dd>
          </div>
        </dl>
      </div>

      <div className="analytics-grid">
        <section className="analytics-panel sources">
          <header>
            <h3>Catalog footprint</h3>
            <p>Distinct entities contributed by each declared repository.</p>
          </header>
          {sources.length === 0 ? (
            <Empty>No repository has declared an entity yet.</Empty>
          ) : (
            <ol className="analytics-ranking">
              {sources.map((source) => (
                <li key={source.repository}>
                  <div>
                    <code>{source.repository}</code>
                    <strong>{number(source.entities)}</strong>
                  </div>
                  <span className="analytics-bar" aria-hidden="true">
                    <span style={{ "--fill": `${Math.max(4, (source.entities / strongest) * 100)}%` } as CSSProperties} />
                  </span>
                </li>
              ))}
            </ol>
          )}
        </section>

        <section className="analytics-panel knowledge">
          <header>
            <h3>Knowledge reach</h3>
            <p>Notes ranked by how many things they connect.</p>
          </header>
          {knowledge.length === 0 ? (
            <Empty>No knowledge has been written down yet.</Empty>
          ) : (
            <div className="analytics-knowledge">
              {knowledge.map((note) => (
                <article key={note.id}>
                  <div className="analytics-note-head">
                    <span className={`tag kind-${note.kind}`}>{note.kind}</span>
                    {note.pinned && <span className="analytics-pinned">in context</span>}
                    <span>{note.links} {note.links === 1 ? "connection" : "connections"}</span>
                  </div>
                  <div className="analytics-note-copy"><Markdown excerpt>{opening(note.body, 160)}</Markdown></div>
                  <button type="button" onClick={() => onOpenNote(note.id)}>Open note</button>
                </article>
              ))}
            </div>
          )}
        </section>

        <section className="analytics-panel plugins">
          <header>
            <h3>Plugin activity</h3>
            <p>Invocations in the retained local action window.</p>
          </header>
          {plugins.length === 0 ? (
            <Empty>No plugin actions have been recorded yet.</Empty>
          ) : (
            <ol className="analytics-plugins">
              {plugins.map((plugin, index) => (
                <li key={plugin.plugin}>
                  <span className="analytics-rank">{String(index + 1).padStart(2, "0")}</span>
                  <div>
                    <strong>{plugin.plugin}</strong>
                    <span>{outcome(plugin.succeeded, plugin.problems, plugin.actions)}</span>
                  </div>
                  <div className="analytics-plugin-count">
                    <strong>{plugin.actions}</strong>
                    <span>{when(plugin.last_used)}</span>
                  </div>
                </li>
              ))}
            </ol>
          )}
        </section>
      </div>

      <footer className="analytics-footer">
        <ul className="analytics-kinds" aria-label="Note kinds">
          {(snapshot.note_kinds ?? []).map((kind) => (
            <li key={kind.kind}><strong>{kind.count}</strong> {kind.kind}</li>
          ))}
        </ul>
        <p>Computed inside Dusk from the current catalog and retained action history. Nothing is sent out.</p>
      </footer>
    </div>
  );
}

function Empty({ children }: { children: string }) {
  return <p className="analytics-empty">{children}</p>;
}

function number(value: number): string {
  return new Intl.NumberFormat().format(value);
}

function outcome(succeeded: number, problems: number, total: number): string {
  if (problems > 0) return `${succeeded} succeeded, ${problems} need attention`;
  if (succeeded > 0) return `${succeeded} of ${total} succeeded`;
  return "No completed outcomes yet";
}

function when(value?: string): string {
  if (!value) return "not dated";
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? value : at.toLocaleDateString();
}
