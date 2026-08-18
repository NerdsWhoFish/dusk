package web_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/NerdsWhoFish/dusk/web"
)

// A fixture rather than a real catalog, which renders whatever it happens to
// hold: that is how a matrix passes while a phone breaks.
const (
	// Long enough to widen any track that forgot minmax(0, 1fr), which is the
	// defect that broke every phone once.
	fixtureRef = "service:platform/checkout-api-gateway-replication-eu-west"

	fixtureKind = "message-broker-subscription"

	// A markdown fence cannot be written inside a Go raw string.
	fence = "```"
)

func fixtureHome() string {
	prose := strings.Join([]string{
		"Everything Dusk has been told about, and everything it has seen.",
		"",
		"| Column | Another column | A third one |",
		"| --- | --- | --- |",
		"| a value | another value | a third value |",
		"",
		fence + "bash",
		"dusk validate --repository example/platform --ref refs/heads/main --verbose",
		fence,
	}, `\n`)

	return fmt.Sprintf(`{
  "title": "The catalog",
  "prose": %q,
  "search": true,
  "proof": "proof-home",
  "repositories": [
    {"Repository":"example/platform","GitRef":"","Commit":"0f4c1ab9d2e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0","Entities":128,"Relations":214,"Error":"","Participating":true},
    {"Repository":"example/lab","GitRef":"","Commit":"7d881ef9d2e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0","Entities":42,"Relations":60,"Error":"","Participating":true}
  ],
  "blocks": [
    {"type":"kinds","title":"Kinds","kinds":[
      {"Kind":"service","Count":128},
      {"Kind":"host","Count":42},
      {"Kind":%q,"Count":9},
      {"Kind":"svc","Count":3},
      {"Kind":"airport","Count":1406},
      {"Kind":"country","Count":195}
    ]},
    {"type":"entities","title":"Recently declared","entities":[
      {"ref":%q,"kind":"service","namespace":"platform","name":"checkout-api-gateway-replication-eu-west","title":"Checkout API gateway"},
      {"ref":"host:platform/build-runner-arm64-large-0007","kind":"host","namespace":"platform","name":"build-runner-arm64-large-0007"}
    ],"truncated":true},
    {"type":"recent-notes","title":"Written down lately","notes":[
      {"id":"note/9f2c1b","kind":"gotcha","body":"The replication lag alarm fires on the replica, never on the primary, so paging on the primary means nobody is paged.","pinned":true},
      {"id":"note/1a77de","kind":"todo","body":"Decide whether the gateway keeps its own connection pool.","status":"open"}
    ]},
    {"type":"drift","title":"Drift","drift":[
      {"Kind":"declared_not_observed","Ref":%q,"Title":"Checkout API gateway","Declared":"example/platform","Observed":"","Detail":""},
      {"Kind":"observed_not_declared","Ref":"host:cluster/node-with-a-name-nobody-shortened","Title":"","Declared":"","Observed":"kubernetes","Detail":""}
    ]},
    {"type":"integrity","title":"Integrity","problems":[
      {"Kind":"dangling_relation","Ref":"datastore:platform/checkout-primary","Detail":"declared as a dependency and nothing declares it","Where":["example/platform/services/checkout.md"]}
    ]},
    {"type":"reads","title":"Last read","reads":[
      {"repository":"example/platform","entities":128},
      {"repository":"ingester:kubernetes","entities":1406,"observed":true},
      {"repository":"example/an-organisation-with-a-long-name","entities":0,"error":"the tree could not be read"}
    ]}
  ]
}`, prose, fixtureKind, fixtureRef, fixtureRef)
}

func fixtureEntity() string {
	description := strings.Join([]string{
		"Terminates TLS for the checkout path and fans out to the regional replicas.",
		"",
		"| Region | Replicas | Endpoint |",
		"| --- | --- | --- |",
		"| eu-west | 4 | gateway.eu-west.example.com |",
		"| us-east | 6 | gateway.us-east.example.com |",
	}, `\n`)

	return fmt.Sprintf(`{
  "entity": {
    "ref": %q,
    "kind": "service",
    "namespace": "platform",
    "name": "checkout-api-gateway-replication-eu-west",
    "title": "Checkout API gateway",
    "description": %q,
    "attributes": {
      "url": "https://gateway.eu-west.example.com/health/detailed?include=replicas",
      "owner": "platform",
      "runbook": "https://example.com/runbooks/checkout-api-gateway-replication-eu-west"
    },
    "provenance": {"source": "example/platform", "version": "0f4c1ab9d2e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0"}
  },
  "relations": [
    {"type":"depends_on","from":%[1]q,"to":"datastore:platform/checkout-primary-replica-set"},
    {"type":"runs_on","from":%[1]q,"to":"host:platform/build-runner-arm64-large-0007"},
    {"type":"serves","from":"service:platform/storefront","to":%[1]q}
  ],
  "notes": [
    {"id":"note/9f2c1b","kind":"gotcha","body":"The replication lag alarm fires on the replica, never on the primary.","pinned":true,"provenance":{"source":"services/checkout/dusk.md","version":"0f4c1ab9d2e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0","observed_at":"2026-08-16T09:15:00Z"}}
  ],
  "dependents": [
    {"Ref":"service:platform/storefront","Depth":1},
    {"Ref":"service:platform/order-confirmation-notification-worker-with-a-long-name","Depth":2}
  ],
  "events": [
    {"id":"ev-1","plugin":"kubernetes","ref":%[1]q,"action":"restart","actor":"agent","status":"succeeded","started_at":"2026-08-16T09:20:00Z","finished_at":"2026-08-16T09:20:04Z","message":"the deployment behind this service was restarted"}
  ],
  "sources": [
    {"Repository":"example/platform","Path":"services/checkout/dusk.md","Source":"services/checkout/dusk.md","Version":"0f4c1ab9d2e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0","Observed":false},
    {"Repository":"ingester:plugin:kubernetes","Path":"","Source":"plugin:kubernetes","Version":"refs/dusk/observed","Observed":true}
  ],
  "views": [
    {"plugin":"example","title":"Replicas","spec":{"layout":"table","empty":"No replicas.","fields":[
      {"source":"name","label":"Replica"},
      {"source":"ref","label":"Reference","format":"code"},
      {"source":"owner","label":"Owner"},
      {"source":"url","label":"Endpoint","format":"link"},
      {"source":"runbook","label":"Runbook","format":"code"},
      {"source":"kind","label":"Kind","format":"badge"}
    ]}}
  ],
  "actions": [
    {"plugin":"example","name":"restart","description":"Restart the workload behind this service.","class":"mutating","async":false,"enabled":true,"approval":"confirm"}
  ],
  "proof": "proof-entity"
}`, fixtureRef, description)
}

// stubAPI answers what the three routes read. Every payload is the wire shape
// internal/server writes, so a field renamed there and not here fails as a page
// that never settles rather than as a layout nobody is measuring any more.
func stubAPI() map[string]string {
	return map[string]string{
		"/api/viewer": `{"signed_in":true,"login":"octocat","restricted":true,"readable":3,"github":true}`,

		"/api/kinds": fmt.Sprintf(`{
  "roles": ["infrastructure", "reference"],
  "kinds": [
    {"namespace":"entity","kind":"service","role":"infrastructure","count":128,"minted":true,"aliases":["svc","services"]},
    {"namespace":"entity","kind":"host","role":"infrastructure","count":42},
    {"namespace":"entity","kind":%q,"role":"infrastructure","count":9},
    {"namespace":"entity","kind":"svc","role":"infrastructure","count":3,"alias_of":"service"},
    {"namespace":"entity","kind":"airport","role":"reference","count":1406,"minted":true},
    {"namespace":"entity","kind":"country","role":"reference","count":195,"minted":true}
  ]
}`, fixtureKind),

		"/api/home":                   fixtureHome(),
		"/api/entities/" + fixtureRef: fixtureEntity(),

		"/api/plugins": `{"plugins":[
  {"id":"kubernetes","org":"example","repository":"example/dusk-plugin-kubernetes","description":"Workloads, services and the nodes underneath them, read from a cluster the operator already has credentials for.","url":"https://example.com","version":"v0.4.0","installed":true,"installed_version":"v0.3.1","update_available":true,"running":true,
   "process":{"phase":"running","restarts":3,"since":"2026-08-16T09:00:00Z","exit_at":"","next":""},
   "health":[{"instance":"","entities":1406,"relations":2900,"at":"2026-08-16T09:30:00Z"}]},
  {"id":"airtrail","org":"example","repository":"example/dusk-plugin-airtrail","description":"Flights and the airports they connect.","url":"https://example.com","version":"v0.2.0","installed":true,"installed_version":"v0.2.0","update_available":false,"running":false,
   "process":{"phase":"failed","restarts":8,"attempts":8,"exit":"exit status 1","since":"","exit_at":"2026-08-16T09:10:00Z","next":""},
   "health":[{"instance":"","entities":0,"relations":0,"at":"2026-08-16T08:00:00Z","problem":"the upstream refused the token, so nothing was observed on this run"}]}
]}`,

		"/api/events": `{"events":[
  {"id":"ev-1","plugin":"kubernetes","ref":"` + fixtureRef + `","action":"restart","actor":"agent","status":"succeeded","started_at":"2026-08-16T09:20:00Z","finished_at":"2026-08-16T09:20:04Z","message":"the deployment behind this service was restarted"},
  {"id":"ev-2","plugin":"airtrail","action":"record-flight","actor":"browser","status":"failed","started_at":"2026-08-16T09:05:00Z","message":"the upstream refused the token"}
]}`,
	}
}

// fixtureHandler serves the built UI and the stub API from one origin, which is
// what lets the harness read the frame's layout at all.
func fixtureHandler(shell []byte, files http.Handler, root http.FileSystem) http.Handler {
	api := stubAPI()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := api[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = io.WriteString(w, body)
			return
		}

		// An unstubbed read means the fixture has fallen behind the UI. Serving
		// the shell instead would render an error panel narrower than what it
		// replaced, and the matrix would pass on a page that never loaded.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, `{"error":"this read is not stubbed"}`, http.StatusNotImplemented)
			return
		}

		if file, err := root.Open(r.URL.Path); err == nil {
			_ = file.Close()
			files.ServeHTTP(w, r)
			return
		}

		// The shell for anything else, as internal/server does, so a deep link
		// to an entity is a page rather than a 404.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(shell)
	})
}

// bundled returns the built UI, or false when this checkout has never run
// `make web`.
func bundled() (http.FileSystem, bool) {
	root, ok := web.Bundle()
	if !ok {
		return nil, false
	}
	return http.FS(root), true
}
