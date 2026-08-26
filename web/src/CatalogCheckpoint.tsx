import { useEffect, useMemo, useState } from "react";

import type { RepositoryStatus } from "./api";

const storageKey = "dusk.catalog-checkpoint.v1";

type SourceState = {
  repository: string;
  gitRef: string;
  commit: string;
  error: string;
  participating: boolean;
};

type Checkpoint = {
  at: string;
  sources: Record<string, SourceState>;
};

type Change = {
  key: string;
  repository: string;
  from?: SourceState;
  to?: SourceState;
};

// The checkpoint belongs to this browser, not to an account Dusk does not
// have. That keeps "since I read this" honest in the one-operator model while
// still requiring an explicit acknowledgement before changes disappear.
export function CatalogCheckpoint({ repositories }: { repositories: RepositoryStatus[] }) {
  const current = useMemo(() => checkpointOf(repositories), [repositories]);
  const [read, setRead] = useState<Checkpoint | null>(() => restore());

  useEffect(() => {
    if (read || repositories.length === 0) {
      return;
    }
    persist(current);
    setRead(current);
  }, [current, read, repositories.length]);

  if (repositories.length === 0 || !read) {
    return null;
  }

  const changes = changed(read, current);
  const markRead = () => {
    persist(current);
    setRead(current);
  };

  return (
    <section className={`checkpoint ${changes.length > 0 ? "checkpoint-changed" : ""}`}>
      <div className="checkpoint-copy">
        <span className="brief-label">Since you last marked it read</span>
        <strong>
          {changes.length === 0
            ? "Catalog sources are unchanged"
            : `${changes.length} catalog ${changes.length === 1 ? "source has" : "sources have"} changed`}
        </strong>
        <p>
          {changes.length === 0
            ? `This browser last marked the catalog read ${when(read.at)}.`
            : "These changes stay here until you mark them read."}
        </p>
      </div>

      {changes.length > 0 && (
        <>
          <ul className="checkpoint-list">
            {changes.map((change) => (
              <li key={change.key}>
                <div>
                  <code>
                    {change.to?.gitRef || change.from?.gitRef
                      ? `${change.repository} @ ${change.to?.gitRef ?? change.from?.gitRef}`
                      : change.repository}
                  </code>
                  <span>{describe(change)}</span>
                </div>
                {changeURL(change) && (
                  <a
                    className="checkpoint-link"
                    href={changeURL(change)}
                    target="_blank"
                    rel="noreferrer"
                    aria-label={`View source changes for ${change.repository}`}
                  >
                    View source changes ↗
                  </a>
                )}
              </li>
            ))}
          </ul>
          <button type="button" className="btn secondary" onClick={markRead}>
            Mark as read
          </button>
        </>
      )}
    </section>
  );
}

function checkpointOf(repositories: RepositoryStatus[]): Checkpoint {
  const sources: Record<string, SourceState> = {};
  for (const repository of repositories) {
    const key = `${repository.Repository}\u0000${repository.GitRef}`;
    sources[key] = {
      repository: repository.Repository,
      gitRef: repository.GitRef,
      commit: repository.Commit,
      error: repository.Error,
      participating: repository.Participating,
    };
  }
  return { at: new Date().toISOString(), sources };
}

function changed(before: Checkpoint, after: Checkpoint): Change[] {
  const keys = new Set([...Object.keys(before.sources), ...Object.keys(after.sources)]);
  return [...keys]
    .filter((key) => JSON.stringify(before.sources[key]) !== JSON.stringify(after.sources[key]))
    .map((key) => ({
      key,
      repository: after.sources[key]?.repository ?? before.sources[key]?.repository ?? key,
      from: before.sources[key],
      to: after.sources[key],
    }))
    .sort((a, b) => a.repository.localeCompare(b.repository));
}

function describe(change: Change): string {
  if (!change.from) {
    return change.to?.error ? `new source, read failed: ${change.to.error}` : "new source";
  }
  if (!change.to) {
    return "no longer installed";
  }
  if (change.to.error && change.to.error !== change.from.error) {
    return `read failed: ${change.to.error}`;
  }
  if (change.from.error && !change.to.error) {
    return "reads successfully again";
  }
  if (change.from.commit !== change.to.commit) {
    return `${short(change.from.commit) || "unread"} to ${short(change.to.commit) || "unread"}`;
  }
  return change.to.participating ? "now participates in the catalog" : "no longer has a dusk.md";
}

function short(commit: string): string {
  return commit.length > 7 ? commit.slice(0, 7) : commit;
}

export function changeURL(change: Change): string | undefined {
  const [owner, name, ...rest] = change.repository.split("/");
  if (!owner || !name || rest.length > 0) {
    return undefined;
  }
  const repository = `https://github.com/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`;
  const before = change.from?.commit;
  const after = change.to?.commit;
  if (before && after && before !== after) {
    return `${repository}/compare/${encodeURIComponent(before)}...${encodeURIComponent(after)}`;
  }
  const commit = after || before;
  return commit ? `${repository}/commit/${encodeURIComponent(commit)}` : undefined;
}

function when(value: string): string {
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? value : at.toLocaleString();
}

function restore(): Checkpoint | null {
  try {
    const raw = localStorage.getItem(storageKey);
    if (!raw) {
      return null;
    }
    const checkpoint = JSON.parse(raw) as Partial<Checkpoint>;
    return checkpoint.at && checkpoint.sources && typeof checkpoint.sources === "object"
      ? (checkpoint as Checkpoint)
      : null;
  } catch {
    return null;
  }
}

function persist(checkpoint: Checkpoint): void {
  try {
    localStorage.setItem(storageKey, JSON.stringify(checkpoint));
  } catch {
    // A locked-down browser still gets the catalog; it simply cannot remember
    // a checkpoint after this page is gone.
  }
}
