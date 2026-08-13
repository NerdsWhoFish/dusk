import { useCallback, useEffect, useState } from "react";
import { api, Unauthorized } from "./api";
import type { Viewer } from "./api";
import { EntityView } from "./EntityView";
import { Landing } from "./Landing";

// The route is the URL, read and written directly. A router is a dependency
// this earns once there are more than two views, and there are two.
function refFromPath(path: string): string | null {
  const ref = path.startsWith("/entity/") ? path.slice("/entity/".length) : "";
  return ref ? decodeURIComponent(ref) : null;
}

export function App() {
  const [ref, setRef] = useState(() => refFromPath(location.pathname));
  const [viewer, setViewer] = useState<Viewer | null>(null);

  useEffect(() => {
    const onPop = () => setRef(refFromPath(location.pathname));
    addEventListener("popstate", onPop);
    return () => removeEventListener("popstate", onPop);
  }, []);

  useEffect(() => {
    api.viewer().then(setViewer).catch(() => setViewer(null));
  }, []);

  const open = useCallback((next: string | null) => {
    history.pushState(null, "", next ? `/entity/${encodeURIComponent(next)}` : "/");
    setRef(next);
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
          <form method="post" action="/logout">
            <button className="signout" type="submit">
              Sign out
            </button>
          </form>
        </div>
      </header>

      {ref ? <EntityView entityRef={ref} onOpen={open} /> : <Landing onOpen={open} />}
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
