import { useCallback, useEffect, useState } from "react";
import { api, catalogURL, previewRef, Unauthorized } from "./api";
import type { Viewer } from "./api";
import { Context } from "./Context";
import { EntityView } from "./EntityView";
import { Landing } from "./Landing";
import { Menu } from "./Menu";
import { NoteView } from "./NoteView";
import { Plugins } from "./Plugins";

// The route is the URL, read and written directly. These views need no route
// matching beyond a handful of exact paths and two path prefixes.
function refFromPath(path: string): string | null {
  const ref = path.startsWith("/entity/") ? path.slice("/entity/".length) : "";
  return ref ? decodeURIComponent(ref) : null;
}

function noteFromPath(path: string): string | null {
  const id = path.startsWith("/note/") ? path.slice("/note/".length) : "";
  return id ? decodeURIComponent(id) : null;
}

function previewTitle(ref: string): string {
  const parts = ref.split("/");
  const repository = parts.slice(2, -2).join("/");
  return `${repository ? `${repository}, ` : ""}PR #${parts.at(-2)}`;
}

export function App() {
  const [ref, setRef] = useState(() => refFromPath(location.pathname));
  const [note, setNote] = useState(() => noteFromPath(location.pathname));
  const [path, setPath] = useState(() => location.pathname);
  const [navigation, setNavigation] = useState(0);
  const [viewer, setViewer] = useState<Viewer | null>(null);
  const [viewerReady, setViewerReady] = useState(false);
  const preview = previewRef();

  useEffect(() => {
    const onPop = () => {
      setNavigation((current) => current + 1);
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
    setNavigation((current) => current + 1);
    const target = next ? `/entity/${encodeURIComponent(next)}` : "/";
    history.pushState(null, "", catalogURL(target));
    setRef(next);
    setNote(null);
    setPath(target);
    scrollTo(0, 0);
  }, []);

  const openNote = useCallback((next: string) => {
    setNavigation((current) => current + 1);
    const target = `/note/${encodeURIComponent(next)}`;
    history.pushState(null, "", catalogURL(target));
    setRef(null);
    setNote(next);
    setPath(target);
    scrollTo(0, 0);
  }, []);

  const go = useCallback((target: string) => {
    setNavigation((current) => current + 1);
    history.pushState(null, "", catalogURL(target));
    setRef(null);
    setNote(null);
    setPath(target);
    scrollTo(0, 0);
  }, []);

  return (
    <div className={`shell ${path === "/context" ? "shell-wide" : ""}`}>
      <header className="top">
        <a
          className="brand"
          href={catalogURL("/")}
          onClick={(e) => {
            e.preventDefault();
            open(null);
          }}
        >
          Dusk
        </a>
        <div className="who">
          <Menu path={path} onGo={go} />
        </div>
      </header>

      <main>
        {preview && (
          <aside className="preview-banner" aria-label="Catalog preview">
            <strong>Read-only preview</strong>
            <code>{previewTitle(preview)}</code>
            <a href={path}>Return to the live catalog</a>
          </aside>
        )}
        {!viewerReady ? null : preview && (path === "/plugins" || path === "/context") ? (
          <section>
            <h1>Preview browsing</h1>
            <p>Plugin management and agent context configuration are available in the live catalog.</p>
          </section>
        ) : path === "/plugins" ? (
          <Plugins />
        ) : path === "/context" ? (
          <Context />
        ) : ref ? (
          <EntityView key={`${preview}:${ref}`} entityRef={ref} onOpen={open} />
        ) : note ? (
          <NoteView key={`${preview}:${note}`} noteId={note} onBack={() => open(null)} />
        ) : (
          <Landing
            key={navigation}
            cacheScope={`${viewer?.cache_scope ?? ""}:${preview}`}
            onOpen={open}
            onOpenNote={openNote}
          />
        )}
      </main>
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
