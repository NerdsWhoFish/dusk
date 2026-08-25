import { useCallback, useEffect, useState } from "react";
import { api, Unauthorized } from "./api";
import type { Viewer } from "./api";
import { EntityView } from "./EntityView";
import { Landing } from "./Landing";
import { Menu } from "./Menu";
import { NoteView } from "./NoteView";
import { Plugins } from "./Plugins";

// The route is the URL, read and written directly. A router is a dependency
// this earns once there are more than three views, and there are three.
function refFromPath(path: string): string | null {
  const ref = path.startsWith("/entity/") ? path.slice("/entity/".length) : "";
  return ref ? decodeURIComponent(ref) : null;
}

function noteFromPath(path: string): string | null {
  const id = path.startsWith("/note/") ? path.slice("/note/".length) : "";
  return id ? decodeURIComponent(id) : null;
}

export function App() {
  const [ref, setRef] = useState(() => refFromPath(location.pathname));
  const [note, setNote] = useState(() => noteFromPath(location.pathname));
  const [path, setPath] = useState(() => location.pathname);
  const [viewer, setViewer] = useState<Viewer | null>(null);
  const [viewerReady, setViewerReady] = useState(false);

  useEffect(() => {
    const onPop = () => {
      setRef(refFromPath(location.pathname));
      setNote(noteFromPath(location.pathname));
      setPath(location.pathname);
    };
    addEventListener("popstate", onPop);
    return () => removeEventListener("popstate", onPop);
  }, []);

  useEffect(() => {
    api
      .viewer()
      .then(setViewer)
      .catch(() => setViewer(null))
      .finally(() => setViewerReady(true));
  }, []);

  const open = useCallback((next: string | null) => {
    const target = next ? `/entity/${encodeURIComponent(next)}` : "/";
    history.pushState(null, "", target);
    setRef(next);
    setNote(null);
    setPath(target);
    scrollTo(0, 0);
  }, []);

  const openNote = useCallback((next: string) => {
    const target = `/note/${encodeURIComponent(next)}`;
    history.pushState(null, "", target);
    setRef(null);
    setNote(next);
    setPath(target);
    scrollTo(0, 0);
  }, []);

  const go = useCallback((target: string) => {
    history.pushState(null, "", target);
    setRef(null);
    setNote(null);
    setPath(target);
    scrollTo(0, 0);
  }, []);

  return (
    <div className="shell">
      <header className="top">
        <a
          className="brand"
          href="/"
          onClick={(e) => {
            e.preventDefault();
            open(null);
          }}
        >
          Dusk
        </a>
        {/* A filtered view that says nothing looks like an empty catalog,
            which is how every silent permission system confuses people. */}
        {viewer?.restricted && (
          <span className="viewer" title={`${viewer.readable} repositories readable`}>
            {viewer.login} · showing what you can read
          </span>
        )}
        <div className="who">
          <Menu path={path} onGo={go} />
        </div>
      </header>

      {!viewerReady ? null : path === "/plugins" ? (
        <Plugins />
      ) : ref ? (
        <EntityView entityRef={ref} onOpen={open} />
      ) : note ? (
        <NoteView noteId={note} onBack={() => open(null)} />
      ) : (
        <Landing
          cacheScope={viewer?.cache_scope ?? ""}
          onOpen={open}
          onOpenNote={openNote}
        />
      )}
    </div>
  );
}

// handle turns a lapsed session into a trip to the login page rather than an
// error the person cannot do anything about.
export function handle(setProblem: (message: string) => void) {
  return (error: unknown) => {
    if (error instanceof Unauthorized) {
      location.href = "/login";
      return;
    }
    setProblem(error instanceof Error ? error.message : String(error));
  };
}
