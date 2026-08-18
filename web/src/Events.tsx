import { useEffect, useState } from "react";

import { api } from "./api";
import type { Event } from "./api";

// Events is what has been run. The bounded history and retry receipts are
// durable even though the catalog index beside them is disposable.
export function Events() {
  const [events, setEvents] = useState<Event[]>();
  const [problem, setProblem] = useState<string>();

  useEffect(() => {
    let live = true;
    api
      .events(25)
      .then((answer) => live && setEvents(answer.events ?? []))
      .catch((error: unknown) => {
        if (live) {
          setProblem(error instanceof Error ? error.message : String(error));
        }
      });

    return () => {
      live = false;
    };
  }, []);

  if (problem) {
    return (
      <p className="hint err" role="alert">
        {problem}
      </p>
    );
  }
  if (!events) {
    return <div className="skeleton" style={{ height: "4rem" }} aria-hidden="true" />;
  }
  if (events.length === 0) {
    return <p className="quiet">Nothing has been run yet.</p>;
  }

  return (
    <div className="events">
      {events.map((event) => (
        <article className="event" key={event.id}>
          <span className={`tag status-${event.status}`}>{event.status}</span>
          <strong>{event.action}</strong>
          {event.ref && <code>{event.ref}</code>}
          <span className="quiet">
            {event.plugin}
            {event.actor ? ` · ${event.actor}` : ""}
          </span>
          <span className="event-when">{when(event.finished_at ?? event.started_at)}</span>
          {event.message && <span className="event-message">{event.message}</span>}
        </article>
      ))}
    </div>
  );
}

function when(value?: string): string {
  if (!value) {
    return "";
  }
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? value : at.toLocaleTimeString();
}
