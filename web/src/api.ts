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

export const api = {
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
