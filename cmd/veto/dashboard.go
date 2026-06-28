package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/oleg-koval/veto/pkg/router"
)

// dashboard serves a local web page that shows the routing pipeline live via
// server-sent events, plus the accumulated decision history from history.json.
type dashboard struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
	backlog []string // replayed to browsers that connect mid-route
}

func newDashboard() *dashboard {
	return &dashboard{clients: make(map[chan string]struct{})}
}

// OnEvent broadcasts a routing event to every connected browser.
func (d *dashboard) OnEvent(e router.ProgressEvent) {
	payload, _ := json.Marshal(map[string]any{
		"kind":       string(e.Kind),
		"model":      e.Model,
		"reasons":    e.Reasons,
		"detail":     compactError(e.Detail),
		"confidence": e.Confidence,
		"est_cost":   e.EstCost,
	})
	d.broadcast("event", string(payload))
}

// sendResult pushes the final outcome so the page can render success/failure.
func (d *dashboard) sendResult(model, tier string, saved float64, ok bool) {
	payload, _ := json.Marshal(map[string]any{
		"model": model, "tier": tier, "saved": saved, "ok": ok,
	})
	d.broadcast("result", string(payload))
}

func (d *dashboard) broadcast(typ, data string) {
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", typ, data)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.backlog = append(d.backlog, msg)
	for c := range d.clients {
		select {
		case c <- msg:
		default: // slow client — drop rather than block routing
		}
	}
}

func (d *dashboard) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan string, 128)
	d.mu.Lock()
	for _, m := range d.backlog {
		ch <- m
	}
	d.clients[ch] = struct{}{}
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		delete(d.clients, ch)
		d.mu.Unlock()
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			fmt.Fprint(w, msg)
			flusher.Flush()
		}
	}
}

// serveHistory returns per-model accept/reject counts from the persisted history.
func serveHistory(w http.ResponseWriter, _ *http.Request) {
	type rec struct {
		Kind     string `json:"kind"`
		Model    string `json:"model"`
		Accepted bool   `json:"accepted"`
	}
	out := map[string]map[string]int{}
	if data, err := os.ReadFile(historyPath()); err == nil {
		var recs []rec
		if json.Unmarshal(data, &recs) == nil {
			for _, r := range recs {
				if r.Kind != "decision" {
					continue
				}
				if out[r.Model] == nil {
					out[r.Model] = map[string]int{}
				}
				if r.Accepted {
					out[r.Model]["accepted"]++
				} else {
					out[r.Model]["rejected"]++
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// start binds a free local port, serves the dashboard, and returns its URL.
func (d *dashboard) start(task, kind, risk string) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	page := dashboardHTML
	page = strings.ReplaceAll(page, "{{TASK}}", htmlEscape(task))
	page = strings.ReplaceAll(page, "{{KIND}}", htmlEscape(kind))
	page = strings.ReplaceAll(page, "{{RISK}}", htmlEscape(risk))

	mux := http.NewServeMux()
	mux.HandleFunc("/events", d.handleEvents)
	mux.HandleFunc("/history", serveHistory)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, page)
	})
	go func() { _ = http.Serve(ln, mux) }()
	return "http://" + ln.Addr().String(), nil
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

const dashboardHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>veto — routing dashboard</title>
<style>
  :root { color-scheme: dark; }
  body { font: 14px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
         background:#0d1117; color:#c9d1d9; margin:0; padding:2rem; }
  h1 { font-size:1.1rem; margin:0 0 .25rem; color:#e6edf3; }
  .muted { color:#7d8590; }
  .task { background:#161b22; border:1px solid #30363d; border-radius:8px;
          padding:1rem 1.25rem; margin-bottom:1.5rem; }
  .grid { display:grid; grid-template-columns:1fr 1fr; gap:1.5rem; }
  @media (max-width:760px){ .grid{ grid-template-columns:1fr; } }
  .panel h2 { font-size:.8rem; text-transform:uppercase; letter-spacing:.08em;
              color:#7d8590; margin:0 0 .75rem; }
  .row { display:flex; align-items:center; gap:.6rem; padding:.35rem .5rem;
         border-bottom:1px solid #21262d; }
  .name { width:9rem; color:#e6edf3; }
  .badge { font-size:.75rem; padding:.05rem .5rem; border-radius:99px; }
  .pass{ color:#3fb950; } .skip{ color:#7d8590; }
  .accept{ background:#1a3326; color:#3fb950; }
  .reject{ background:#3d1d1d; color:#f85149; }
  .err{ background:#3d2d12; color:#d29922; }
  #result { margin-top:1.5rem; font-size:1rem; }
  .ok { color:#3fb950; } .fail { color:#f85149; }
  .saved { color:#58a6ff; }
  table { width:100%; border-collapse:collapse; }
  td,th { text-align:left; padding:.25rem .5rem; border-bottom:1px solid #21262d; }
  th { color:#7d8590; font-weight:400; font-size:.75rem; }
</style>
</head>
<body>
  <h1>veto routing</h1>
  <div class="task">
    <div>“{{TASK}}”</div>
    <div class="muted">kind: {{KIND}} · risk: {{RISK}}</div>
  </div>

  <div class="grid">
    <div class="panel">
      <h2>Live pipeline</h2>
      <div id="filter"></div>
      <div id="ask" style="margin-top:1rem"></div>
      <div id="result"></div>
    </div>
    <div class="panel">
      <h2>History (all runs)</h2>
      <table id="history"><thead><tr><th>model</th><th>accepted</th><th>rejected</th></tr></thead><tbody></tbody></table>
    </div>
  </div>

<script>
const filter = document.getElementById('filter');
const ask = document.getElementById('ask');
const result = document.getElementById('result');
const spinFrames = ['⠋','⠙','⠹','⠸','⠼','⠴','⠦','⠧','⠇','⠏'];
const spinTimers = {};

function row(parent, model, html) {
  let el = document.getElementById('m-' + parent + '-' + model);
  if (!el) {
    el = document.createElement('div');
    el.className = 'row';
    el.id = 'm-' + parent + '-' + model;
    (parent === 'f' ? filter : ask).appendChild(el);
  }
  el.innerHTML = '<span class="name">' + model + '</span>' + html;
  return el;
}

function stopSpin(model) {
  if (spinTimers[model]) {
    clearInterval(spinTimers[model]);
    delete spinTimers[model];
  }
}

const es = new EventSource('/events');
es.addEventListener('event', e => {
  const d = JSON.parse(e.data);
  switch (d.kind) {
    case 'filter_pass':
      row('f', d.model, '<span class="pass">pass</span>');
      break;
    case 'filter_fail':
      row('f', d.model, '<span class="skip">skip · ' + (d.reasons||[]).join(', ') + '</span>');
      break;
    case 'ask_start': {
      let i = 0;
      const el = row('a', d.model, '<span class="spin-char">' + spinFrames[0] + '</span> <span class="muted">asking…</span>');
      spinTimers[d.model] = setInterval(() => {
        const ch = el.querySelector('.spin-char');
        if (ch) ch.textContent = spinFrames[++i % spinFrames.length];
      }, 80);
      break;
    }
    case 'ask_accept':
      stopSpin(d.model);
      row('a', d.model, '<span class="badge accept">✓ accepted ' + Math.round(d.confidence*100) + '%</span>');
      break;
    case 'ask_reject':
      stopSpin(d.model);
      row('a', d.model, '<span class="badge reject">✗ ' + (d.reasons||[]).join(', ') + '</span>');
      break;
    case 'ask_error':
      stopSpin(d.model);
      row('a', d.model, '<span class="badge err">! ' + (d.detail||'error') + '</span>');
      break;
  }
});
es.addEventListener('result', e => {
  const d = JSON.parse(e.data);
  if (d.ok) {
    let s = '<span class="ok">→ Selected: ' + d.model + ' (' + d.tier + ' tier)</span>';
    if (d.saved > 0) s += ' <span class="saved">· saved ~$' + d.saved.toFixed(4) + ' vs opus</span>';
    result.innerHTML = s;
  } else {
    result.innerHTML = '<span class="fail">No model accepted this task.</span>';
  }
  loadHistory();
});

function loadHistory() {
  const tb = document.querySelector('#history tbody');
  tb.innerHTML = '<tr><td colspan="3" class="muted">loading…</td></tr>';
  fetch('/history').then(r => r.json()).then(h => {
    tb.innerHTML = '';
    const models = Object.keys(h).sort();
    if (models.length === 0) {
      tb.innerHTML = '<tr><td colspan="3" class="muted">no history yet</td></tr>';
      return;
    }
    models.forEach(m => {
      const tr = document.createElement('tr');
      tr.innerHTML = '<td>' + m + '</td><td>' + (h[m].accepted||0) + '</td><td>' + (h[m].rejected||0) + '</td>';
      tb.appendChild(tr);
    });
  }).catch(() => {
    tb.innerHTML = '<tr><td colspan="3" class="muted">could not load history</td></tr>';
  });
}
loadHistory();
</script>
</body>
</html>`
