import { useCallback, useEffect, useState } from "react";
import { api, Unauthorized } from "./api";
import type { Viewer } from "./api";
import { EntityView } from "./EntityView";
import { Landing } from "./Landing";
import { Menu } from "./Menu";
import { Plugins } from "./Plugins";

// The route is the URL, read and written directly. A router is a dependency
// this earns once there are more than three views, and there are three.
function refFromPath(path: string): string | null {
  const ref = path.startsWith("/entity/") ? path.slice("/entity/".length) : "";
  return ref ? decodeURIComponent(ref) : null;
}

export function App() {
  const [ref, setRef] = useState(() => refFromPath(location.pathname));
  const [path, setPath] = useState(() => location.pathname);
  const [viewer, setViewer] = useState<Viewer | null>(null);

  useEffect(() => {
    const onPop = () => {
      setRef(refFromPath(location.pathname));
      setPath(location.pathname);
    };
    addEventListener("popstate", onPop);
    return () => removeEventListener("popstate", onPop);
  }, []);

  useEffect(() => {
    api.viewer().then(setViewer).catch(() => setViewer(null));
  }, []);

  const open = useCallback((next: string | null) => {
    const target = next ? `/entity/${encodeURIComponent(next)}` : "/";
    history.pushState(null, "", target);
    setRef(next);
    setPath(target);
    scrollTo(0, 0);
  }, []);

  const go = useCallback((target: string) => {
    history.pushState(null, "", target);
    setRef(null);
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

      {path === "/plugins" ? (
        <Plugins />
      ) : ref ? (
        <EntityView entityRef={ref} onOpen={open} />
      ) : (
        <Landing onOpen={open} />
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
