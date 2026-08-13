import { useEffect, useRef, useState } from "react";

// Menu holds everything that is not the catalog itself. The header had four
// things competing for 390 pixels, and this is the one that scales as more
// pages arrive.
export function Menu({ path, onGo }: { path: string; onGo: (target: string) => void }) {
  const [open, setOpen] = useState(false);
  const container = useRef<HTMLDivElement>(null);

  // Closing on an outside click and on Escape is what makes it a menu rather
  // than a panel that has to be un-clicked exactly where it was opened.
  useEffect(() => {
    if (!open) {
      return;
    }

    const onPointer = (event: MouseEvent) => {
      if (!container.current?.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
      }
    };

    addEventListener("mousedown", onPointer);
    addEventListener("keydown", onKey);
    return () => {
      removeEventListener("mousedown", onPointer);
      removeEventListener("keydown", onKey);
    };
  }, [open]);

  const go = (target: string) => {
    setOpen(false);
    onGo(target);
  };

  return (
    <div className="menu" ref={container}>
      <button
        type="button"
        className="menu-button"
        aria-label="Menu"
        aria-expanded={open}
        aria-haspopup="true"
        onClick={() => setOpen((was) => !was)}
      >
        <span className="menu-bars" aria-hidden="true" />
      </button>

      {open && (
        <div className="menu-items" role="menu">
          <button
            type="button"
            role="menuitem"
            className="menu-item"
            aria-current={path !== "/plugins" ? "page" : undefined}
            onClick={() => go("/")}
          >
            Catalog
          </button>
          <button
            type="button"
            role="menuitem"
            className="menu-item"
            aria-current={path === "/plugins" ? "page" : undefined}
            onClick={() => go("/plugins")}
          >
            Plugins
          </button>

          <form method="post" action="/logout" className="menu-form">
            <button type="submit" role="menuitem" className="menu-item">
              Sign out
            </button>
          </form>
        </div>
      )}
    </div>
  );
}
