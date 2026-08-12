export type Entity = {
  ref: string;
  kind: string;
  namespace: string;
  name: string;
  title?: string;
  description?: string;
  attributes?: Record<string, unknown>;
  provenance?: { source?: string; version?: string; observed_at?: string };
};

export type Relation = { type: string; from: string; to: string };

export type Note = { id: string; kind: string; body: string; pinned?: boolean };

export type SearchResult = {
  Type: string;
  Ref: string;
  Kind: string;
  Title: string;
  Snippet: string;
};

export type EntityDetail = {
  entity: Entity;
  relations: Relation[];
  notes: Note[];
};

export type RepositoryStatus = {
  Repository: string;
  GitRef: string;
  Commit: string;
  Entities: number;
  Relations: number;
  Error: string;
  Participating: boolean;
};

// Unauthorized means the session lapsed rather than the request being wrong,
// so it is a distinct type: the app sends the person to sign in again instead
// of showing them an error they cannot act on.
export class Unauthorized extends Error {}

async function get<T>(path: string): Promise<T> {
  const response = await fetch(`/api${path}`, {
    headers: { Accept: "application/json" },
  });

  if (response.status === 401) {
    throw new Unauthorized("session expired");
  }
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new Error(body.error ?? `the catalog returned ${response.status}`);
  }
  return response.json() as Promise<T>;
}

export type KindCount = { Kind: string; Count: number };

// Overview is the portal's whole payload. One shape, one request: the landing
// page is the first thing loaded and a waterfall there is what people feel.
export type Overview = {
  kinds: KindCount[];
  total: number;
  declaring: number;
  notes: Note[];
  repositories: RepositoryStatus[];
};

export type Drift = {
  Kind: "declared_not_observed" | "observed_not_declared";
  Ref: string;
  Title: string;
  Declared: string;
  Observed: string;
  Detail: string;
};

export type Problem = {
  Kind: string;
  Ref: string;
  Detail: string;
  Where: string[];
};

export type Read = {
  repository: string;
  entities: number;
  error?: string;
  observed?: boolean;
};

// ResolvedBlock is one declared query with its result. The server runs the
// query (ADR-0035), so the browser only decides how a result looks.
export type ResolvedBlock = {
  type: "entities" | "recent-notes" | "drift" | "integrity" | "kinds" | "reads";
  title: string;
  wide?: boolean;
  entities?: Entity[];
  notes?: Note[];
  drift?: Drift[];
  problems?: Problem[];
  kinds?: KindCount[];
  reads?: Read[];
  truncated?: boolean;
  error?: string;
};

export type Home = {
  title: string;
  prose: string;
  blocks: ResolvedBlock[];
  problem?: string;
};

export type Viewer = {
  signed_in: boolean;
  login?: string;
  restricted: boolean;
  readable?: number;
  github: boolean;
};

export const api = {
  viewer: () => get<Viewer>("/viewer"),
  home: () => get<Home>("/home"),
  drift: () => get<{ drift: Drift[] }>("/drift"),
  overview: () => get<Overview>("/overview"),
  entities: (kind?: string) =>
    get<{ entities: Entity[] }>(
      kind ? `/entities?kind=${encodeURIComponent(kind)}` : "/entities",
    ),
  search: (query: string) =>
    get<{ results: SearchResult[] }>(`/search?q=${encodeURIComponent(query)}`),
  entity: (ref: string) =>
    get<EntityDetail>(`/entities/${encodeURIComponent(ref)}`),
  status: () => get<{ repositories: RepositoryStatus[] }>("/status"),
};
