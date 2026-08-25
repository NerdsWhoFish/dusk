import { useCallback, useEffect, useState } from "react";

import { handle } from "./App";
import { api, type NoteDetail } from "./api";
import { Notes } from "./Notes";

export function NoteView({
  noteId,
  onBack,
}: {
  noteId: string;
  onBack: () => void;
}) {
  const [detail, setDetail] = useState<NoteDetail | null>(null);
  const [problem, setProblem] = useState<string | null>(null);

  const load = useCallback(() => {
    let live = true;
    setProblem(null);
    api
      .note(noteId, {
        onFresh: (data) => live && setDetail(data),
        onError: handle((message) => live && setProblem(message)),
      })
      .then((data) => live && setDetail(data))
      .catch(handle((message) => live && setProblem(message)));
    return () => {
      live = false;
    };
  }, [noteId]);

  useEffect(() => {
    setDetail(null);
    return load();
  }, [load]);

  const back = (
    <a
      className="back"
      href="/"
      onClick={(event) => {
        event.preventDefault();
        onBack();
      }}
    >
      ← Back to the catalog
    </a>
  );

  if (problem) {
    return (
      <>
        {back}
        <p className="problem">{problem}</p>
      </>
    );
  }
  if (!detail) {
    return (
      <>
        {back}
        <div className="skeleton" style={{ height: "10rem" }} aria-hidden="true" />
      </>
    );
  }

  return (
    <>
      {back}
      <header className="identity note-identity">
        <div className="title">
          <h1>{detail.note.kind}</h1>
          {detail.note.pinned && <span className="tag pinned">pinned</span>}
        </div>
        <p className="ref">{detail.note.id}</p>
      </header>

      <Notes notes={[detail.note]} proof={detail.proof} onChanged={load} />
    </>
  );
}
