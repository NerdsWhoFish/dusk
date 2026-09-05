import { useState } from "react";

import { api, catalogURL, previewRef } from "./api";
import type { Closed, Note } from "./api";
import { Markdown } from "./Markdown";

// Working are the note kinds a status means something for. A gotcha is never
// done; an idea is the thing somebody finishes or decides against.
const working = new Set(["idea", "todo"]);

// Notes renders what has been written down. A note that is work carries what
// closes it, so an idea can be finished where it is read rather than by
// finding the file it lives in.
export function Notes({
  notes,
  proof,
  compact,
  onOpenNote,
  onChanged,
}: {
  notes: Note[];
  proof?: string;
  compact?: boolean;
  onOpenNote?: (id: string) => void;
  onChanged?: () => void;
}) {
  if (notes.length === 0) {
    return null;
  }

  return (
    <>
      {notes.map((note) => (
        <Written
          key={note.id}
          note={note}
          proof={proof}
          compact={compact}
          onOpenNote={onOpenNote}
          onChanged={onChanged}
        />
      ))}
    </>
  );
}

// opening keeps a note in a panel to its first paragraph. The whole thing lives
// on the entity it is about; here it is a pointer, not the text.
export function opening(body: string, limit = 240): string {
  const first = body.trim().split(/\n\s*\n/, 1)[0] ?? "";
  return first.length > limit ? `${first.slice(0, limit)}...` : first;
}

function Written({
  note,
  proof,
  compact,
  onOpenNote,
  onChanged,
}: {
  note: Note;
  proof?: string;
  compact?: boolean;
  onOpenNote?: (id: string) => void;
  onChanged?: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string>();
  const [proposed, setProposed] = useState<Closed>();

  const closed = note.status === "done" || note.status === "dropped";
  const closable = working.has(note.kind) && !closed && Boolean(proof) && Boolean(onChanged) && !previewRef();
  const prefetch = () => void api.prefetchNote(note.id).catch(() => undefined);

  const close = async (status: "done" | "dropped") => {
    setBusy(true);
    setProblem(undefined);
    try {
      const answer = await api.closeNote(note.id, status, proof);

      // Nothing was committed, so nothing is refreshed: the note is still open
      // and what comes back is the change somebody has to apply themselves.
      if (answer.proposed) {
        setProposed(answer);
        return;
      }
      onChanged?.();
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  };

  return (
    <article className={`note ${compact ? "note-compact" : ""} ${closed ? "closed" : ""}`}>
      <div className="note-head">
        <div className="note-tags">
          <span className="tag note-kind">{note.kind}</span>
          {closed && <span className={`tag status-${note.status}`}>{note.status}</span>}
        </div>
        {compact && onOpenNote && (
          <a
            className="note-open"
            href={catalogURL(`/note/${encodeURIComponent(note.id)}`)}
            onMouseEnter={prefetch}
            onFocus={prefetch}
            onTouchStart={prefetch}
            onClick={(event) => {
              event.preventDefault();
              onOpenNote(note.id);
            }}
          >
            Open note <span aria-hidden="true">→</span>
          </a>
        )}
      </div>

      <Markdown excerpt={compact}>{compact ? opening(note.body) : note.body}</Markdown>

      {problem && (
        <p className="hint err" role="alert">
          {problem}
        </p>
      )}

      {proposed && (
        <div className="prose" role="status">
          <p className="hint">
            Nothing was committed: Dusk may not write to your repositories. Apply this to{" "}
            <code>{proposed.path}</code> in {proposed.repository}.
          </p>
          <pre>{proposed.diff}</pre>
        </div>
      )}

      {!compact && note.provenance && (
        <p className="note-source">
          Written in <code>{note.provenance.source || "the catalog"}</code>
          {note.provenance.version ? ` at ${shortVersion(note.provenance.version)}` : ""}
          {note.provenance.observed_at ? ` · read ${when(note.provenance.observed_at)}` : ""}
        </p>
      )}

      {!compact && <p className="ref note-id">{note.id}</p>}

      {closable && (
        <div className="note-close">
          <button type="button" className="btn secondary" disabled={busy} onClick={() => close("done")}>
            Done
          </button>
          <button type="button" className="btn secondary" disabled={busy} onClick={() => close("dropped")}>
            Drop it
          </button>
        </div>
      )}
    </article>
  );
}

function shortVersion(version: string): string {
  return /^[0-9a-f]{8,}$/i.test(version) ? version.slice(0, 7) : version;
}

function when(value: string): string {
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? value : at.toLocaleDateString();
}
