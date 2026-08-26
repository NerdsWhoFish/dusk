import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";

import { handle } from "./App";
import { Markdown } from "./Markdown";
import {
  api,
  type ContextPreview,
  type Note,
  type NotePage,
  type RepositoryStatus,
  type WriteResult,
} from "./api";

type Editor = { note: Note; creating: boolean };

const notePageSize = 100;

const emptyNote: Note = {
  id: "",
  kind: "gotcha",
  body: "",
  refs: [],
  pinned: true,
  status: "open",
};

export function Context() {
  const [root, setRoot] = useState("");
  const [activeRoot, setActiveRoot] = useState("");
  const [preview, setPreview] = useState<ContextPreview | null>(null);
  const [repositories, setRepositories] = useState<RepositoryStatus[]>([]);
  const [notePage, setNotePage] = useState<NotePage | null>(null);
  const [noteProofs, setNoteProofs] = useState<Record<string, string>>({});
  const [notesBusy, setNotesBusy] = useState(false);
  const [problem, setProblem] = useState<string>();
  const [notice, setNotice] = useState<string>();
  const [raw, setRaw] = useState(false);
  const [policyOpen, setPolicyOpen] = useState(false);
  const [policy, setPolicy] = useState("");
  const [policyBusy, setPolicyBusy] = useState(false);
  const [query, setQuery] = useState("");
  const [editor, setEditor] = useState<Editor>();
  const [deleting, setDeleting] = useState<Note>();
  const [noteBusy, setNoteBusy] = useState(false);
  const [proposal, setProposal] = useState<WriteResult>();

  const loadContext = useCallback((scope: string) => {
    setProblem(undefined);
    return api
      .context(scope)
      .then((data) => {
        setPreview(data);
        setPolicy(data.profile.body);
        setActiveRoot(scope);
      })
      .catch(handle(setProblem));
  }, []);

  const loadNotes = useCallback((offset = 0) => {
    setNotesBusy(true);
    return api
      .notes(notePageSize, offset)
      .then((page) => {
        setNotePage((current) =>
          offset === 0 || !current
            ? page
            : { ...page, offset: 0, notes: [...current.notes, ...page.notes] },
        );
        setNoteProofs((current) => {
          const next: Record<string, string> = offset === 0 ? {} : { ...current };
          if (page.proof) {
            for (const note of page.notes) {
              next[note.id] = page.proof;
            }
          }
          return next;
        });
      })
      .catch(handle(setProblem))
      .finally(() => setNotesBusy(false));
  }, []);

  useEffect(() => {
    void loadContext("");
    void loadNotes(0);
    void api.status().then((data) => setRepositories(data.repositories)).catch(handle(setProblem));
  }, [loadContext, loadNotes]);

  const notes = useMemo(() => {
    const wanted = query.trim().toLowerCase();
    const all = notePage?.notes ?? [];
    if (!wanted) {
      return all;
    }
    return all.filter((note) =>
      [note.id, note.kind, note.body, ...(note.refs ?? [])]
        .join("\n")
        .toLowerCase()
        .includes(wanted),
    );
  }, [notePage, query]);

  const showContext = (event: FormEvent) => {
    event.preventDefault();
    void loadContext(root.trim());
  };

  const savePolicy = async () => {
    if (!preview) {
      return;
    }
    setPolicyBusy(true);
    setProblem(undefined);
    setNotice(undefined);
    setProposal(undefined);
    try {
      const result = await api.setContext(policy, preview.profile.proof);
      if (result.proposed) {
        setProposal(result);
      } else {
        setNotice("Context policy committed. The exact preview updates after Dusk reconciles that commit.");
      }
    } catch (error) {
      handle(setProblem)(error);
    } finally {
      setPolicyBusy(false);
    }
  };

  const updateLocal = (note: Note, result: WriteResult) => {
    if (result.proposed) {
      setProposal(result);
      return;
    }
    setNotePage((page) => {
      if (!page) {
        return page;
      }
      const exists = page.notes.some((item) => item.id === note.id);
      const saved = { ...note, id: note.id || result.ref };
      return {
        ...page,
        total: exists ? page.total : page.total + 1,
        notes: exists
          ? page.notes.map((item) => (item.id === note.id ? saved : item))
          : [saved, ...page.notes],
      };
    });
    setNotice("Knowledge committed. Agent context changes when Dusk reconciles that commit.");
  };

  const pin = async (note: Note) => {
    setNoteBusy(true);
    setProblem(undefined);
    setProposal(undefined);
    try {
      const changed = { ...note, pinned: !note.pinned };
      const result = await api.writeNote(changed, noteProofs[note.id]);
      updateLocal(changed, result);
    } catch (error) {
      handle(setProblem)(error);
    } finally {
      setNoteBusy(false);
    }
  };

  const saveNote = async (note: Note) => {
    setNoteBusy(true);
    setProblem(undefined);
    setProposal(undefined);
    try {
      const result = await api.writeNote(note, note.id ? noteProofs[note.id] : undefined);
      updateLocal(note, result);
      setEditor(undefined);
    } catch (error) {
      handle(setProblem)(error);
    } finally {
      setNoteBusy(false);
    }
  };

  const removeNote = async () => {
    if (!deleting) {
      return;
    }
    setNoteBusy(true);
    setProblem(undefined);
    setProposal(undefined);
    try {
      const result = await api.deleteNote(deleting.id, noteProofs[deleting.id]);
      if (result.proposed) {
        setProposal(result);
      } else {
        setNotePage((page) =>
          page
            ? {
                ...page,
                total: Math.max(0, page.total - 1),
                notes: page.notes.filter((note) => note.id !== deleting.id),
              }
            : page,
        );
        setNotice("Note deleted from Git. It leaves agent context after reconciliation.");
      }
      setDeleting(undefined);
    } catch (error) {
      handle(setProblem)(error);
    } finally {
      setNoteBusy(false);
    }
  };

  const ratio = preview ? Math.min(100, (preview.bytes / preview.budget) * 100) : 0;

  return (
    <main className="context-page">
      <header className="context-hero">
        <div>
          <p className="eyebrow">Agent lens</p>
          <h1>See the session before it starts.</h1>
          <p>
            This is the exact Markdown returned by <code>dusk_context</code>. Pinning and edits
            below use the same Git-backed write path agents use.
          </p>
        </div>
        <div className="context-orbit" aria-hidden="true">
          <span />
        </div>
      </header>

      <form className="context-scope" onSubmit={showContext}>
        <label htmlFor="context-root">Repository scope</label>
        <div>
          <input
            id="context-root"
            list="context-repositories"
            value={root}
            onChange={(event) => setRoot(event.target.value)}
            placeholder="Whole estate, or owner/name"
          />
          <datalist id="context-repositories">
            {repositories.map((repository) => (
              <option key={repository.Repository} value={repository.Repository} />
            ))}
          </datalist>
          <button className="btn" type="submit">
            Preview
          </button>
        </div>
        <p>
          {activeRoot ? <code>{`dusk_context({ root: "${activeRoot}" })`}</code> : "Whole-estate context"}
        </p>
      </form>

      {problem && <p className="hint err context-alert" role="alert">{problem}</p>}
      {notice && <p className="hint context-success" role="status">{notice}</p>}
      {proposal && <Proposal result={proposal} />}

      <div className="context-grid">
        <section className="context-preview" aria-busy={!preview}>
          <header>
            <div>
              <p className="eyebrow">Exact output</p>
              <strong>{preview?.repository || "Entire catalog"}</strong>
            </div>
            <div className="context-tabs" aria-label="Preview format">
              <button type="button" className={!raw ? "on" : ""} onClick={() => setRaw(false)}>
                Rendered
              </button>
              <button type="button" className={raw ? "on" : ""} onClick={() => setRaw(true)}>
                Raw
              </button>
            </div>
          </header>

          {preview ? (
            <>
              <div className="context-budget">
                <span style={{ width: `${ratio}%` }} />
              </div>
              <p className="context-budget-copy">
                {preview.bytes.toLocaleString()} of {preview.budget.toLocaleString()} bytes
              </p>
              <div className={`context-document ${raw ? "raw" : ""}`}>
                {raw ? <pre>{preview.context}</pre> : <Markdown>{preview.context}</Markdown>}
              </div>
            </>
          ) : (
            <div className="skeleton tall" aria-hidden="true" />
          )}
        </section>

        <aside className="context-controls">
          <section className="context-panel">
            <button
              type="button"
              className="context-panel-head"
              aria-expanded={policyOpen}
              onClick={() => setPolicyOpen((open) => !open)}
            >
              <span>
                <span className="eyebrow">Policy</span>
                <strong>{preview?.profile.path ?? ".dusk/context.md"}</strong>
              </span>
              <span aria-hidden="true">{policyOpen ? "−" : "+"}</span>
            </button>
            {policyOpen && (
              <div className="context-policy">
                <p>
                  The whole file. Invalid profiles are refused before Git changes.
                </p>
                <textarea
                  aria-label="Agent context policy"
                  value={policy}
                  onChange={(event) => setPolicy(event.target.value)}
                  spellCheck={false}
                />
                <button className="btn" type="button" disabled={policyBusy} onClick={savePolicy}>
                  {policyBusy ? "Saving..." : "Save policy"}
                </button>
              </div>
            )}
          </section>

          <section className="context-panel knowledge-panel">
            <header className="knowledge-head">
              <div>
                <p className="eyebrow">Knowledge</p>
                <strong>
                  {notePage
                    ? notePage.notes.length < notePage.total
                      ? `${notePage.notes.length} of ${notePage.total} notes`
                      : `${notePage.total} notes`
                    : "Loading notes"}
                </strong>
              </div>
              <button
                type="button"
                className="btn secondary"
                onClick={() => setEditor({ note: { ...emptyNote }, creating: true })}
              >
                New note
              </button>
            </header>
            <input
              className="note-filter"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Filter notes"
              aria-label="Filter notes"
            />
            <div className="managed-notes">
              {notes.map((note) => (
                <article key={note.id} className={`managed-note ${note.pinned ? "is-pinned" : ""}`}>
                  <div className="managed-note-copy">
                    <span className="tag note-kind">{note.kind}</span>
                    {note.pinned && <span className="tag pinned">in context</span>}
                    <p>{opening(note.body)}</p>
                    <code>{note.id}</code>
                  </div>
                  <div className="managed-note-actions">
                    <button type="button" disabled={noteBusy} onClick={() => void pin(note)}>
                      {note.pinned ? "Unpin" : "Pin"}
                    </button>
                    <button type="button" disabled={noteBusy} onClick={() => setEditor({ note, creating: false })}>
                      Edit
                    </button>
                    <button className="danger" type="button" disabled={noteBusy} onClick={() => setDeleting(note)}>
                      Delete
                    </button>
                  </div>
                  {deleting?.id === note.id && (
                    <div className="note-delete-confirm" role="alertdialog" aria-label={`Delete ${note.id}`}>
                      <p>This removes the file and its knowledge from every future agent session.</p>
                      <div>
                        <button className="btn danger" type="button" disabled={noteBusy} onClick={() => void removeNote()}>
                          Delete note
                        </button>
                        <button className="btn secondary" type="button" onClick={() => setDeleting(undefined)}>
                          Cancel
                        </button>
                      </div>
                    </div>
                  )}
                </article>
              ))}
              {notePage && notes.length === 0 && <p className="quiet">No notes match that filter.</p>}
            </div>
            {notePage && notePage.notes.length < notePage.total && (
              <button
                className="btn secondary notes-more"
                type="button"
                disabled={notesBusy}
                onClick={() => void loadNotes(notePage.notes.length)}
              >
                {notesBusy
                  ? "Loading..."
                  : `Load ${Math.min(notePageSize, notePage.total - notePage.notes.length)} more`}
              </button>
            )}
          </section>
        </aside>
      </div>

      {editor && (
        <NoteEditor
          editor={editor}
          busy={noteBusy}
          onCancel={() => setEditor(undefined)}
          onSave={(note) => void saveNote(note)}
        />
      )}
    </main>
  );
}

function NoteEditor({
  editor,
  busy,
  onCancel,
  onSave,
}: {
  editor: Editor;
  busy: boolean;
  onCancel: () => void;
  onSave: (note: Note) => void;
}) {
  const [note, setNote] = useState<Note>({ ...editor.note, refs: [...(editor.note.refs ?? [])] });
  const [refs, setRefs] = useState((editor.note.refs ?? []).join("\n"));

  const submit = (event: FormEvent) => {
    event.preventDefault();
    onSave({
      ...note,
      refs: refs.split("\n").map((ref) => ref.trim()).filter(Boolean),
    });
  };

  return (
    <div className="editor-backdrop">
      <form className="note-editor" role="dialog" aria-modal="true" aria-labelledby="note-editor-title" onSubmit={submit}>
        <header>
          <div>
            <p className="eyebrow">{editor.creating ? "New knowledge" : "Edit knowledge"}</p>
            <h2 id="note-editor-title">{editor.creating ? "Write a note" : note.id}</h2>
          </div>
          <button type="button" className="editor-close" aria-label="Close editor" onClick={onCancel}>×</button>
        </header>

        <div className="note-editor-fields">
          <label>
            <span>Kind</span>
            <input required value={note.kind} onChange={(event) => setNote({ ...note, kind: event.target.value })} />
          </label>
          <label>
            <span>Status</span>
            <select value={note.status ?? "open"} onChange={(event) => setNote({ ...note, status: event.target.value as Note["status"] })}>
              <option value="open">Open</option>
              <option value="done">Done</option>
              <option value="dropped">Dropped</option>
            </select>
          </label>
          <label className="note-editor-body">
            <span>Markdown body</span>
            <textarea required value={note.body} onChange={(event) => setNote({ ...note, body: event.target.value })} />
          </label>
          <label className="note-editor-refs">
            <span>Entity refs, one per line</span>
            <textarea value={refs} onChange={(event) => setRefs(event.target.value)} placeholder="service:home/example" />
          </label>
          <label className="note-editor-pin">
            <input type="checkbox" checked={Boolean(note.pinned)} onChange={(event) => setNote({ ...note, pinned: event.target.checked })} />
            <span className="note-editor-check" aria-hidden="true" />
            <span>Pin into future agent context</span>
          </label>
        </div>
        <footer>
          <button className="btn" type="submit" disabled={busy}>{busy ? "Saving..." : "Save note"}</button>
          <button className="btn secondary" type="button" onClick={onCancel}>Cancel</button>
        </footer>
      </form>
    </div>
  );
}

function Proposal({ result }: { result: WriteResult }) {
  return (
    <div className="context-proposal" role="status">
      <p>
        Dusk may not write to <code>{result.repository}</code>. Apply this change to <code>{result.path}</code>.
      </p>
      <pre>{result.diff}</pre>
    </div>
  );
}

function opening(body: string): string {
  const first = body.trim().split(/\n\s*\n/, 1)[0] ?? "";
  return first.length > 150 ? `${first.slice(0, 150)}...` : first;
}
