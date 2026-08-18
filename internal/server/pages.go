package server

// pages holds the onboarding templates. Server rendered on purpose: /setup is
// a form POSTing to github.com, and must work before any credential exists.
const pages = `
{{define "head"}}<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" href="/apple-touch-icon.png">
<title>Dusk</title>
<style>
:root{--bg:#22212C;--fg:#F8F8F2;--muted:#7970A9;--line:#3B3549;
--purple:#BD93F9;--green:#50FA7B;--red:#FF5555;--yellow:#FFCA80;--card:#2A2735}
*{box-sizing:border-box}
body{margin:0;padding:1.25rem;background:var(--bg);color:var(--fg);
font:16px/1.6 ui-sans-serif,system-ui,-apple-system,Segoe UI,Roboto,sans-serif;
overflow-x:hidden}
main{max-width:40rem;margin:0 auto}
h1{font-size:1.5rem;margin:0 0 .25rem;color:var(--purple)}
h2{font-size:1rem;margin:1.75rem 0 .5rem;color:var(--fg)}
p{margin:.5rem 0;color:var(--fg)}
.sub{color:var(--muted);margin-bottom:1.5rem}
.card{background:var(--card);border:1px solid var(--line);border-radius:.5rem;
padding:1rem;margin:1rem 0}
label{display:block;margin:.75rem 0 .25rem;color:var(--muted);font-size:.875rem}
input,select{width:100%;min-height:44px;padding:.5rem .625rem;font:inherit;
background:var(--bg);color:var(--fg);border:1px solid var(--line);border-radius:.375rem}
input:focus,select:focus{outline:2px solid var(--purple);outline-offset:1px}
button,.btn{display:block;width:100%;min-height:44px;margin-top:1.25rem;
padding:.75rem 1rem;font:inherit;font-weight:600;text-align:center;text-decoration:none;
background:var(--purple);color:#22212C;border:0;border-radius:.375rem;cursor:pointer}
button:focus,.btn:focus{outline:2px solid var(--fg);outline-offset:2px}
.perm{display:flex;justify-content:space-between;gap:1rem;padding:.375rem 0;
border-bottom:1px solid var(--line);font-size:.9375rem}
.perm:last-child{border-bottom:0}
.perm span:last-child{color:var(--green);white-space:nowrap}
code{background:var(--bg);border:1px solid var(--line);border-radius:.25rem;
padding:.125rem .375rem;font-size:.875rem;word-break:break-all}
.err{color:var(--red)} .warn{color:var(--yellow)}
.hint{color:var(--muted);font-size:.8125rem;margin:.375rem 0 0}
.detail{background:var(--bg);border:1px solid var(--line);border-radius:.375rem;
padding:.75rem;overflow-x:auto;font-size:.875rem;white-space:pre-wrap;word-break:break-word}
.btn.secondary{background:none;color:var(--fg);border:1px solid var(--line)}
.or{text-align:center;margin:1.25rem 0 0}
@media(min-width:48rem){body{padding:3rem 1.5rem}h1{font-size:2rem}
button,.btn{width:auto;min-width:14rem}}
</style></head><body><main>{{end}}

{{define "foot"}}</main></body></html>{{end}}

{{define "setup"}}{{template "head"}}
<h1>Set up Dusk</h1>
<p class="sub">Dusk is built for one trusted operator and their agents. It
registers its own GitHub App; you choose which repositories make up your
homelab catalog. Nothing is created until you confirm on GitHub.</p>

<form method="get" action="/setup">
  <div class="card">
    <label for="mode">Access mode</label>
    <select id="mode" name="mode" onchange="this.form.submit()">
      <option value="read"{{if eq .Mode "read"}} selected{{end}}>Read only</option>
      <option value="proposal"{{if eq .Mode "proposal"}} selected{{end}}>Propose changes as pull requests</option>
      <option value="write"{{if eq .Mode "write"}} selected{{end}}>Write directly</option>
    </select>
    <label for="org">Owner (leave blank for your personal account)</label>
    <input id="org" name="org" value="{{.Org}}" placeholder="my-org" autocapitalize="off" autocorrect="off">
    <p class="hint">The App is created private to this owner. To use Dusk across
    several orgs, make the App public in its GitHub settings afterwards and
    install it wherever you need. You do not have to decide now.</p>
    <noscript><button type="submit">Apply</button></noscript>
  </div>
</form>

<h2>This App will request</h2>
<div class="card">
  {{range $name, $level := .Permissions}}<div class="perm"><span>{{$name}}</span><span>{{$level}}</span></div>{{end}}
</div>

<form method="post" action="{{.Action}}?state={{.State}}">
  <input type="hidden" name="manifest" value='{{.Manifest}}'>
  <button type="submit">Create the App on GitHub</button>
</form>

<h2>Before you continue</h2>
{{if .SplitHosts}}<p class="sub">GitHub will deliver webhooks to
<code>{{.WebhookURL}}</code> and send your browser back to
<code>{{.Base}}/setup/callback</code>. Those are deliberately different hosts.</p>
{{end}}<p class="sub">GitHub will send you back to <code>{{.Base}}/setup/callback</code>.
If that is not how you reach Dusk, fix <code>DUSK_PRIVATE_HOST</code> first, or the
callback will fail. Setup must finish within an hour.</p>
{{template "foot"}}{{end}}

{{define "done"}}{{template "head"}}
<h1>App created</h1>
<p class="sub">{{.Name}} is registered. One step left: install it on the
repositories you want in the catalog.</p>

<div class="card">
  <div class="perm"><span>App</span><span>{{.Slug}}</span></div>
  <div class="perm"><span>Mode</span><span>{{.Mode}}</span></div>
  {{range $name, $level := .Permissions}}<div class="perm"><span>{{$name}}</span><span>{{$level}}</span></div>{{end}}
</div>

<a class="btn" href="{{.InstallURL}}">Install on repositories</a>

<h2>Keep your encryption key</h2>
<p class="sub warn">These credentials are encrypted with
<code>DUSK_ENCRYPTION_KEY</code>. Lose that key and they are unrecoverable, and
you will have to register a new App.</p>
{{template "foot"}}{{end}}

{{define "installed"}}{{template "head"}}
<h1>Dusk is connected</h1>
<p class="sub">Dusk is reading the repositories you selected. A repository
with a <code>dusk.md</code> becomes part of the catalog automatically.</p>

<h2>Get the first result</h2>
<p>Add this file at the root of one selected repository and push it:</p>
<pre class="detail">---
dusk: v1alpha1
namespace: home
kind: service
name: home-assistant
title: Home Assistant
---

The center of the smart home.</pre>
<p class="hint">The installation webhook starts a read immediately. Dusk also
starts one from this page, so a private, poll-only install does not wait for the
daily safety sweep.</p>

<a class="btn" href="/">Open the catalog</a>
<a class="btn secondary" href="https://github.com/NerdsWhoFish/dusk/blob/main/docs/getting-started.md">Read the install guide</a>
{{template "foot"}}{{end}}

{{define "fail"}}{{template "head"}}
<h1 class="err">{{.Title}}</h1>
<p class="sub">Dusk stopped here rather than guessing.</p>
{{if .Detail}}<div class="detail">{{.Detail}}</div>{{end}}
{{if .Retry}}<a class="btn" href="{{.Retry}}">Start again</a>{{end}}
{{template "foot"}}{{end}}

{{define "login"}}{{template "head"}}
<h1>Dusk</h1>
<p class="sub">You and your agents share this catalog and its operational memory.
The browser is behind the same token as the agent surface.</p>

<form method="post" action="/login">
  <div class="card">
    <label for="token">Access token</label>
    <input id="token" name="token" type="password" autocomplete="current-password"
           autofocus required spellcheck="false">
    {{if .Problem}}<p class="hint err" role="alert">{{.Problem}}</p>{{end}}
    <p class="hint">This is <code>DUSK_MCP_TOKEN</code>, the same value an agent
    sends as a bearer token.</p>
  </div>
  <button type="submit">Sign in</button>
</form>

{{if .GitHub}}
<p class="sub or">or</p>
<a class="btn secondary" href="/auth/github">Sign in with GitHub</a>
<p class="hint">Signing in with GitHub shows you only what the repositories you
can read have declared, rather than the whole catalog.</p>
{{end}}
{{template "foot"}}{{end}}

`
