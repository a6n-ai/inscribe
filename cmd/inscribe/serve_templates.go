package main

// pageTemplates holds every page of inscribe serve. The CSP allows inline styles
// and no scripts, so the pages are plain forms and links.
const pageTemplates = `
{{define "head"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Inscribe · {{.}}</title>
<style>
:root { color-scheme: light dark; --fg: #1d1d1f; --mute: #6e6e73; --line: #d2d2d7; --card: #f5f5f7; --accent: #0a66c2; --bad: #b3261e; --good: #1e7d32; }
@media (prefers-color-scheme: dark) { :root { --fg: #f5f5f7; --mute: #a1a1a6; --line: #3a3a3c; --card: #1c1c1e; --accent: #64a8ff; --bad: #ff6b60; --good: #5fd068; } }
* { box-sizing: border-box; }
body { font: 15px/1.5 system-ui, sans-serif; color: var(--fg); max-width: 64rem; margin: 0 auto; padding: 0 1rem 3rem; }
nav { display: flex; gap: 1.25rem; align-items: baseline; border-bottom: 1px solid var(--line); padding: 1rem 0; margin-bottom: 1.5rem; flex-wrap: wrap; }
nav b { margin-right: 1rem; }
a { color: var(--accent); text-decoration: none; }
a:hover, a:focus-visible { text-decoration: underline; }
h1 { font-size: 1.5rem; margin: 0 0 1rem; }
h2 { font-size: 1.1rem; margin: 2rem 0 0.5rem; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(12rem, 1fr)); gap: 0.75rem; }
.card { background: var(--card); border-radius: 0.6rem; padding: 0.75rem 1rem; }
.card .n { font-size: 1.5rem; font-variant-numeric: tabular-nums; }
.mute { color: var(--mute); }
.bad { color: var(--bad); } .good { color: var(--good); }
table { border-collapse: collapse; width: 100%; font-variant-numeric: tabular-nums; }
th, td { padding: 0.35rem 0.75rem 0.35rem 0; text-align: left; vertical-align: top; border-bottom: 1px solid var(--line); }
th { font-weight: 600; color: var(--mute); font-size: 0.85rem; }
td.num, th.num { text-align: right; }
code, .mono { font: 0.85rem ui-monospace, monospace; overflow-wrap: anywhere; }
form.inline { display: flex; gap: 0.5rem; flex-wrap: wrap; align-items: end; margin: 0 0 1rem; }
label { display: grid; gap: 0.2rem; font-size: 0.85rem; color: var(--mute); }
input, select, button { font: inherit; padding: 0.35rem 0.5rem; border: 1px solid var(--line); border-radius: 0.4rem; background: transparent; color: var(--fg); }
input[type=text], input[type=search] { min-width: 16rem; }
button { background: var(--accent); color: #fff; border-color: var(--accent); cursor: pointer; }
button.danger { background: var(--bad); border-color: var(--bad); }
.note { background: var(--card); border-left: 3px solid var(--accent); padding: 0.5rem 0.75rem; border-radius: 0.3rem; }
.settings td:first-child { width: 14rem; }
ol li, ul li { margin: 0.3rem 0; }
</style>
</head>
<body>
<nav><b>Inscribe</b><a href="/">Overview</a><a href="/recall">Recall</a><a href="/memories">Memories</a><a href="/discover">Discover</a><a href="/how">How it works</a><a href="/settings">Settings</a></nav>
{{end}}

{{define "foot"}}</body>
</html>
{{end}}

{{define "overview"}}{{template "head" "Overview"}}
<h1>How inscribe is doing</h1>
<form class="inline" method="get" action="/">
<label>Window<select name="days">{{range $d := (list "7" "14" "30" "90")}}<option value="{{$d}}"{{if eq $d (printf "%d" $.Days)}} selected{{end}}>last {{$d}} days</option>{{end}}</select></label>
<button type="submit">Show</button>
</form>
<p class="mute">All projects, last {{.Days}} days, {{.M.Sessions}} sessions.</p>
<div class="grid">
{{range .Rates}}<div class="card"><div class="mute">{{.Source}} rated helpful</div><div class="n">{{pct .Hits .Misses}}</div><div class="mute">{{.Hits}} of {{add .Hits .Misses}}</div></div>
{{end}}<div class="card"><div class="mute">memories used</div><div class="n">{{.M.MemoriesUsed}}/{{.M.MemoriesActive}}</div><div class="mute">active memories shown at least once</div></div>
<div class="card"><div class="mute">next-try fixes</div><div class="n">{{.M.NextTryFixes}}/{{.M.NextTryShown}}</div><div class="mute">shown fixes followed by a success</div></div>
<div class="card"><div class="mute">wrong</div><div class="n">{{.M.Wrong}}/{{.M.Rated}}</div><div class="mute">rated lines called wrong</div></div>
</div>

<h2>Ratings by source</h2>
<table><tr><th>source</th><th class="num">helpful</th><th class="num">not helpful</th><th class="num">rate</th><th>why not helpful</th></tr>
{{range .Rates}}<tr><td>{{.Source}}</td><td class="num">{{.Hits}}</td><td class="num">{{.Misses}}</td><td class="num">{{pct .Hits .Misses}}</td><td class="mute">{{.Reasons}}</td></tr>
{{end}}</table>
<p class="mute">{{.M.Unlabeled}} shown lines are not rated yet. Feedback asks answered: {{.M.AsksAnswered}}/{{.M.Asks}}.</p>

<h2>Tokens and ratings by hook</h2>
<table><tr><th>hook</th><th class="num">tokens</th><th class="num">rated</th><th class="num">helpful</th><th class="num">tokens per helpful line</th></tr>
{{range .Hooks}}<tr><td>{{.Hook}}</td><td class="num">{{.Tokens}}</td><td class="num">{{.Rated}}</td><td class="num">{{.Helpful}}</td><td class="num">{{if .Helpful}}{{div .Tokens .Helpful}}{{else if .Rated}}<span class="bad">none helpful</span>{{else}}<span class="mute">not rated</span>{{end}}</td></tr>
{{else}}<tr><td colspan="5" class="mute">nothing injected in this window</td></tr>{{end}}</table>
<p class="mute">Only prompt lines get rated. Session-start and subagent lines cost tokens but are never measured.</p>

<h2>Most shown memories</h2>
<table><tr><th>memory</th><th class="num">times shown</th></tr>
{{range .Top}}<tr><td><a href="/memories/{{.ID}}">{{short .Text 110}}</a>{{if .Scope}}<div class="mono mute">{{short .Scope 70}}</div>{{end}}</td><td class="num">{{.N}}</td></tr>
{{else}}<tr><td colspan="2" class="mute">none</td></tr>{{end}}</table>

<h2>Store</h2>
<div class="grid">
{{range .Status}}<div class="card"><div class="mute">{{.Name}}</div><div class="n">{{.N}}</div></div>{{end}}
<div class="card"><div class="mute">database</div><div class="n">{{mb .M.DBBytes}}</div><div class="mute">{{.M.Events}} events</div></div>
</div>
<p class="mute">By type: {{range $i, $t := .Type}}{{if $i}}, {{end}}{{$t.Name}} {{$t.N}}{{end}}</p>

<h2>Score model</h2>
<p class="mute">Recall scores each memory as a weighted blend of how the prompt matches it. The weights are refit from rated lines after sessions end.</p>
<table><tr><th>feature</th><th class="num">weight</th></tr>
{{range .Weights}}<tr><td>{{.Name}}</td><td class="num">{{printf "%.3f" .Value}}</td></tr>
{{end}}</table>
<p class="mute">trained on {{.Seen}} groups: {{.Hits}} hits, {{.Misses}} misses · label_rule {{.LabelRule}}, step_rule {{.StepRule}}</p>

<h2>Share</h2>
<p>analytics: {{if .Share.Analytics}}on{{else}}off{{end}} · share host: {{.Share.ShareHost}} · <a href="/settings">change</a></p>
{{template "foot"}}{{end}}

{{define "recall"}}{{template "head" "Recall"}}
<h1>Try recall</h1>
<p class="mute">See which lines inscribe would add to a prompt. This preview does not count as a use and writes no ratings.</p>
<form class="inline" method="get" action="/recall">
<label>Prompt<input type="search" name="q" value="{{.Q}}" placeholder="fix the login redirect" autofocus></label>
<label>Project folder (optional)<input type="text" name="project" value="{{.Project}}" placeholder="/path/to/repo"></label>
<button type="submit">Recall</button>
</form>
{{if .Off}}<p class="note">recall.inject is off, so nothing is recalled. Turn it on in <a href="/settings">Settings</a>.</p>{{end}}
{{if not .Project}}<p class="mute">With no project folder, memories from every project can match. Global memories always can.</p>{{end}}
{{if .Ran}}
<table><tr><th class="num">score</th><th>type</th><th>line</th></tr>
{{range .Lines}}<tr><td class="num">{{printf "%.2f" .Score}}</td><td>{{.Type}}</td><td><a href="/memories/{{.MemoryID}}">{{.Text}}</a>{{if .Repo}} <span class="mute">({{.Repo}})</span>{{end}}</td></tr>
{{else}}<tr><td colspan="3" class="mute">no line scores above recall.prompt_threshold</td></tr>{{end}}</table>
{{end}}
{{template "foot"}}{{end}}

{{define "memories"}}{{template "head" "Memories"}}
<h1>Memories</h1>
<form class="inline" method="get" action="/memories">
<label>Search<input type="search" name="q" value="{{.Q}}" placeholder="key, text or project"></label>
<label>Status<select name="status">{{range .Statuses}}<option{{if eq . $.Status}} selected{{end}}>{{.}}</option>{{end}}</select></label>
<label>Type<select name="type"><option value="">any</option>{{range .Types}}<option{{if eq . $.Type}} selected{{end}}>{{.}}</option>{{end}}</select></label>
<label>Scope<select name="scope"><option value="">any</option>{{range (list "global" "project" "file")}}<option{{if eq . $.Scope}} selected{{end}}>{{.}}</option>{{end}}</select></label>
<button type="submit">Filter</button>
</form>
<p class="mute">{{.Total}} match{{if gt .Total (len .Rows)}}, newest {{len .Rows}} shown{{end}}.</p>
<table><tr><th>key</th><th>type</th><th>scope</th><th class="num">conf.</th><th>last seen</th></tr>
{{range .Rows}}<tr><td><a href="/memories/{{.ID}}">{{short .Key 70}}</a><div class="mute">{{short .Text 140}}</div></td><td>{{.Type}}</td><td>{{.ScopeKind}}{{if .Scope}}<div class="mono mute">{{short .Scope 60}}</div>{{end}}</td><td class="num">{{printf "%.2f" .Confidence}}</td><td>{{when .LastSeen}}</td></tr>
{{else}}<tr><td colspan="5" class="mute">no memories match</td></tr>{{end}}</table>
{{template "foot"}}{{end}}

{{define "memory"}}{{template "head" "Memory"}}
<p><a href="/memories">← Memories</a></p>
<h1>{{.M.Key}}</h1>
<p>{{.M.Text}}</p>
<table>
<tr><th>id</th><td class="mono">{{.M.ID}}</td></tr>
<tr><th>type</th><td>{{.M.Type}}</td></tr>
<tr><th>status</th><td>{{.M.Status}}{{if .M.SupersededBy}} by <a href="/memories/{{.M.SupersededBy}}">{{.M.SupersededBy}}</a>{{end}}</td></tr>
<tr><th>scope</th><td>{{.M.ScopeKind}} <span class="mono">{{.M.Scope}}</span></td></tr>
<tr><th>confidence</th><td>{{printf "%.2f" .M.Confidence}}{{if .M.Penalty}} <span class="bad">− {{printf "%.2f" .M.Penalty}} penalty</span>{{end}} · seen {{.M.Count}} times</td></tr>
<tr><th>first / last seen</th><td>{{when .M.FirstSeen}} / {{when .M.LastSeen}}</td></tr>
<tr><th>last used</th><td>{{when .M.LastUsed}}</td></tr>
</table>

<h2>Ratings</h2>
<p>Shown {{.Shown}} times · <span class="good">{{.Helpful}} helpful</span> · <span class="bad">{{.Unhelpful}} not helpful</span> · {{pct .Helpful .Unhelpful}}</p>
<table><tr><th>when</th><th>rating</th><th>by</th></tr>
{{range .Outcomes}}<tr><td>{{when .TS}}</td><td>{{if eq .Label 1}}<span class="good">helpful</span>{{else if eq .Label 0}}<span class="bad">not helpful</span>{{else}}<span class="mute">open</span>{{end}}</td><td>{{.Reason}}</td></tr>
{{else}}<tr><td colspan="3" class="mute">never shown</td></tr>{{end}}</table>

<form method="post" action="/memories/{{.M.ID}}/rate"><p>Rate it: {{template "ratebuttons" .M.ID}}</p></form>
<p class="mute">"Wrong" also lowers its confidence at recall, the way an agent's "wrong" does.</p>

<h2>Evidence</h2>
<p class="mute">The captured events this memory was learned from.</p>
<table><tr><th>when</th><th>kind</th><th>event</th></tr>
{{range .Events}}<tr><td>{{when .TS}}</td><td>{{.Kind}}</td><td class="mono">{{short (eventText .) 240}}</td></tr>
{{else}}<tr><td colspan="3" class="mute">no evidence kept (it may have aged out under retention)</td></tr>{{end}}</table>

{{if ne (printf "%s" .M.Status) "forgotten"}}
<h2>Forget</h2>
<p class="mute">A forgotten memory is never recalled again. <code>inscribe forget --purge</code> also deletes it.</p>
<form method="post" action="/memories/{{.M.ID}}/forget"><button class="danger" type="submit">Forget this memory</button></form>
{{end}}
{{template "foot"}}{{end}}

{{define "ratebuttons"}}<button type="submit" name="answer" value="yes" formaction="/memories/{{.}}/rate">helpful</button> <button type="submit" name="answer" value="no" formaction="/memories/{{.}}/rate">not helpful</button> <button class="danger" type="submit" name="answer" value="wrong" formaction="/memories/{{.}}/rate">wrong</button>{{end}}

{{define "discover"}}{{template "head" "Discover"}}
<h1>Shown often, never helpful</h1>
<p class="mute">Memories injected at least {{.Min}} times in the last {{.Days}} days that no rating in that time called helpful. "Not rated" means they were only shown at session or subagent start, where nothing is rated, so that alone is not proof they are useless.</p>
<form class="inline" method="get" action="/discover">
<label>Window<select name="days">{{range $d := (list "7" "14" "30" "90")}}<option value="{{$d}}"{{if eq $d (printf "%d" $.Days)}} selected{{end}}>last {{$d}} days</option>{{end}}</select></label>
<label>Shown at least<input type="text" inputmode="numeric" name="min" value="{{.Min}}" size="4"></label>
<button type="submit">Show</button>
</form>
{{if .Replayed}}<p class="note">Rated {{.Replayed}} shown command lines from what their sessions ran next.</p>{{end}}
<form method="post" action="/replay"><p><button type="submit">Rate old lines from session history</button> <span class="mute">Command lines shown at session or subagent start, last 90 days: helpful when the session then ran that command and it passed.</span></p></form>
<form method="post" action="/memories/forget">
<input type="hidden" name="days" value="{{.Days}}"><input type="hidden" name="min" value="{{.Min}}">
<table><tr><th></th><th>memory</th><th>type</th><th class="num">shown</th><th class="num">rated</th><th>rate it</th></tr>
{{range .Rows}}<tr><td><input type="checkbox" name="id" value="{{.MemoryID}}" aria-label="forget {{.Key}}"></td><td><a href="/memories/{{.MemoryID}}">{{short .Text 110}}</a><div class="mono mute">{{short .Scope 70}}</div></td><td>{{.Type}}</td><td class="num">{{.Shown}}</td><td class="num">{{if .Rated}}<span class="bad">{{.Rated}}, 0 helpful</span>{{else}}<span class="mute">not rated</span>{{end}}</td><td>{{template "ratebuttons" .MemoryID}}</td></tr>
{{else}}<tr><td colspan="6" class="mute">nothing matches</td></tr>{{end}}</table>
{{if .Rows}}<p><button class="danger" type="submit">Forget checked memories</button></p>{{end}}
</form>
{{template "foot"}}{{end}}

{{define "how"}}{{template "head" "How it works"}}
<h1>How inscribe works</h1>
<p>Inscribe is local memory for coding agents. Everything stays in <code>~/.inscribe/inscribe.db</code>. Capture and recall make no network or model calls.</p>
<h2>1. Capture</h2>
<p>Hooks in the agent's settings send each session's events to inscribe: prompts, commands, file edits, errors. Text passes the scrubber before it is stored, and paths under <code>exclude.paths</code> are skipped.</p>
<h2>2. Extract</h2>
<p>Extractors turn events into typed memories: <code>command</code> (how a project builds and tests), <code>fix</code> (what fixed an error), <code>cochange</code> (files that change together), <code>preference</code>, <code>decision</code>, <code>note</code>, and skill use. When a session ends, a <code>session</code> memory sums it up: what was asked, the files changed, the commands that passed. A later prompt in the same project can recall it instead of searching old transcripts. Commands, fixes and skill use are <em>active</em> at once; other memories start as <em>candidates</em> until the review step promotes them.</p>
<h2>3. Scope</h2>
<p>Each memory has a scope: <em>file</em>, <em>project</em> (a git root), or <em>global</em>. Recall in a project sees that project's memories and every global one. On a prompt it also reads notes, decisions, fixes and project facts from your other projects, scaled by <code>recall.cross_project</code> and labeled with their repo, so only a strong match gets in. Preferences and notes saved as global follow you into every project.</p>
<h2>4. Recall</h2>
<p>Inscribe adds a few short lines at session start, on each prompt, when a subagent starts, and when a known error repeats. A subagent gets only lines its session has not shown yet, within <code>recall.subagent_budget</code>. Each memory gets a score: a weighted blend of how the prompt matches its text, key, body, type, scope, agent, command and recent work, times its confidence and recency. Lines under <code>recall.prompt_threshold</code> are dropped, and at most <code>recall.prompt_max_lines</code> are added to a prompt.</p>
<h2>5. Rate</h2>
<p>After a session, each shown line is labeled helpful or not. The labels come from what happened next (a shown fix followed by success), from the agent answering a feedback ask, and from the review step. Lines called wrong get a penalty.</p>
<h2>6. Learn</h2>
<p>When the agent is idle, inscribe refits the score weights from the labeled lines with a small gradient step, pulled back toward the shipped defaults so a few sessions cannot swing them. Optional consolidation (<code>consolidate.auto</code>) has a model review finished sessions to add, promote, reject and rate memories.</p>
<h2>7. Forget</h2>
<p>Retention ages out old prompt text and events. The review step can mark a memory stale or superseded. You can forget any memory from its page here or with <code>inscribe forget</code>.</p>
<h2>Measuring it</h2>
<p>The Overview shows how often shown lines were rated helpful. <code>stats.holdout</code> holds back a fraction of prompts, so you can compare sessions with and without recall.</p>
{{template "foot"}}{{end}}

{{define "settings"}}{{template "head" "Settings"}}
<h1>Settings</h1>
<p class="mute">Writes <code>{{.Config}}</code>. Only changed keys are written, and saving rewrites the file without its comments. Lists such as <code>exclude.paths</code>, <code>scrub.patterns</code> and <code>consolidate.model</code> stay hand-edited.</p>
{{if .Saved}}<p class="note">Saved. Hooks read the new values on their next event.</p>{{end}}
{{range .Warns}}<p class="note">{{.}}</p>{{end}}
<form method="post" action="/settings">
<table class="settings">
{{range .Rows}}<tr><td><label for="{{.Name}}"><code>{{.Name}}</code></label></td><td>
{{if eq .Kind "bool"}}<input type="checkbox" id="{{.Name}}" name="{{.Name}}"{{if eq .Value "true"}} checked{{end}}>
{{else if eq .Kind "choice"}}{{$v := .Value}}<select id="{{.Name}}" name="{{.Name}}">{{range .Choices}}<option{{if eq . $v}} selected{{end}}>{{.}}</option>{{end}}</select>
{{else}}<input type="text" inputmode="decimal" id="{{.Name}}" name="{{.Name}}" value="{{.Value}}">{{end}}
<div class="mute">{{.Help}}</div></td></tr>
{{end}}</table>
<p><button type="submit">Save settings</button></p>
</form>

<h2>Share</h2>
<p>analytics: {{if .Share.Analytics}}on{{else}}off{{end}}</p>
<p>share host: {{.Share.ShareHost}}</p>
{{if .Share.ShareNote}}<p class="note">{{.Share.ShareNote}}</p>{{end}}
<form method="post" action="/share">
<input type="hidden" name="analytics" value="{{.Share.Next}}">
<button type="submit">{{.Share.Button}}</button>
</form>
{{template "foot"}}{{end}}
`
