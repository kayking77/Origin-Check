"use strict";
// OriginCheck front end: assignments, inbox, similarity report, AI writing report, settings.

const $ = (s, el = document) => el.querySelector(s);
const app = $("#app");
let pollTimer = null;
let infoCache = null;

const COLORS = ["#e11d48", "#7c3aed", "#2563eb", "#059669", "#ea580c", "#db2777", "#0891b2", "#65a30d", "#9333ea", "#dc2626",
  "#0d9488", "#ca8a04", "#4f46e5", "#be123c", "#16a34a", "#c2410c", "#0284c7", "#a21caf", "#15803d", "#b45309"];
const color = rank => COLORS[(rank - 1) % COLORS.length];
const GROUP_COLORS = { "Not Cited or Quoted": "#e11d48", "Missing Quotations": "#ea580c", "Missing Citation": "#7c3aed", "Cited and Quoted": "#16a34a" };

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
function h(html) { const t = document.createElement("template"); t.innerHTML = html.trim(); return t.content.firstElementChild; }
function toast(msg, ms = 2600) {
  const t = $("#toast"); t.textContent = msg; t.hidden = false;
  clearTimeout(toast._t); toast._t = setTimeout(() => (t.hidden = true), ms);
}
async function api(path, opts = {}) {
  opts.headers = { ...(opts.headers || {}), "X-OriginCheck": "1" };
  const r = await fetch("/api" + path, opts);
  let body = null;
  try { body = await r.json(); } catch { }
  if (!r.ok) throw new Error((body && body.error) || ("HTTP " + r.status));
  return body;
}
const post = (path, data) => api(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(data) });
function fmtDate(s) {
  const d = new Date(s); if (isNaN(d) || d.getFullYear() < 2000) return "";
  return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) + ", " + d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
}
function simClass(p) { return p <= 0 ? "sim-0" : p < 25 ? "sim-1" : p < 50 ? "sim-2" : p < 75 ? "sim-3" : "sim-4"; }
function pctText(p) { return p > 0 && p < 1 ? "<1%" : Math.round(p) + "%"; }
function aiText(s) {
  if (!s.aiAvailable) return null;
  if (s.ai === 0) return "0%";
  if (s.ai < 20) return "*%";
  return s.ai + "%";
}
function hostOf(u) { try { return new URL(u).hostname.replace(/^www\./, ""); } catch { return u || ""; } }

function setNav(which) {
  document.querySelectorAll("[data-nav]").forEach(a => a.classList.toggle("active", a.dataset.nav === which));
}

// ---------- Router ----------
async function route() {
  clearInterval(pollTimer); pollTimer = null;
  const parts = location.hash.replace(/^#\/?/, "").split("/");
  try {
    if (parts[0] === "a" && parts[1]) return await inboxPage(parts[1]);
    if (parts[0] === "r" && parts[1]) return await reportPage(parts[1], parts[2] === "ai" ? "ai" : "sim");
    if (parts[0] === "print" && parts[1]) return await printPage(parts[1]);
    if (parts[0] === "settings") return await settingsPage();
    if (parts[0] === "about") return await aboutPage();
    return await homePage();
  } catch (e) {
    app.innerHTML = `<div class="page"><div class="card"><h2>Something went wrong</h2><p>${esc(e.message)}</p><a href="#/">Back to assignments</a></div></div>`;
  }
}
window.addEventListener("hashchange", route);

// ---------- Assignments ----------
async function homePage() {
  setNav("home");
  const list = await api("/assignments");
  app.innerHTML = `<div class="page">
    <div class="row"><div class="grow"><h1>Assignments</h1><p class="sub">Each assignment keeps its students' papers together. Papers are checked against the internet, published papers and every paper stored in your repository.</p></div>
    <button class="primary" id="new">+ New assignment</button></div>
    <div class="alist" id="list"></div></div>`;
  const el = $("#list");
  if (!list || !list.length) el.innerHTML = `<div class="empty">No assignments yet.</div>`;
  for (const a of list || []) {
    const c = h(`<div class="acard"><h3>${esc(a.name)}</h3><div class="muted small">${esc(a.class || "")}</div><div class="spacer"></div><div class="count">${a.count} paper${a.count === 1 ? "" : "s"} · created ${esc(fmtDate(a.created).split(",")[0])}</div></div>`);
    c.onclick = () => (location.hash = "#/a/" + a.id);
    el.append(c);
  }
  $("#new").onclick = () => editAssignment(null);
}

function editAssignment(a) {
  a = a || { name: "", class: "", options: { web: true, publications: true, repository: true, store: true }, filters: { excludeQuotes: false, excludeBib: false, excludeCited: false, excludeSmall: 0 } };
  const m = h(`<div class="modal-bg"><div class="modal" style="max-width:560px">
    <h2 style="margin-top:0">${a.id ? "Assignment settings" : "New assignment"}</h2>
    <label>Assignment name</label><input type="text" id="an" value="${esc(a.name)}" placeholder="e.g. Essay 2: The Industrial Revolution">
    <div class="spacer"></div>
    <label>Class (optional)</label><input type="text" id="ac" value="${esc(a.class)}" placeholder="e.g. History 101">
    <div class="spacer"></div>
    <label>Compare papers against</label>
    <label class="check"><input type="checkbox" id="ow" ${a.options.web ? "checked" : ""}><span>Internet<small>Web search, Wikipedia and the pages they lead to</small></span></label>
    <label class="check"><input type="checkbox" id="op" ${a.options.publications ? "checked" : ""}><span>Periodicals, journals and publications<small>Open scholarly databases (OpenAlex)</small></span></label>
    <label class="check"><input type="checkbox" id="or" ${a.options.repository ? "checked" : ""}><span>Student paper repository<small>Every paper you've stored, from this and other assignments</small></span></label>
    <label class="check"><input type="checkbox" id="os" ${a.options.store ? "checked" : ""}><span>Store new papers in the repository<small>So later papers, from any student, are checked against them</small></span></label>
    <div class="spacer"></div>
    <label>Default report filters</label>
    <label class="check"><input type="checkbox" id="fq" ${a.filters.excludeQuotes ? "checked" : ""}><span>Exclude quoted text</span></label>
    <label class="check"><input type="checkbox" id="fb" ${a.filters.excludeBib ? "checked" : ""}><span>Exclude bibliography</span></label>
    <label class="check"><input type="checkbox" id="fc" ${a.filters.excludeCited ? "checked" : ""}><span>Exclude cited text</span></label>
    <div class="switch"><span>Exclude matches shorter than (words, 0 = off)</span><input type="number" id="fs" min="0" max="100" value="${a.filters.excludeSmall || 0}"></div>
    <div class="spacer"></div>
    <div class="row">${a.id ? `<button class="danger" id="del">Delete assignment</button>` : ""}<div class="grow"></div><button id="cancel">Cancel</button><button class="primary" id="save">Save</button></div>
  </div></div>`);
  document.body.append(m);
  $("#an", m).focus();
  $("#cancel", m).onclick = () => m.remove();
  m.onclick = e => { if (e.target === m) m.remove(); };
  if (a.id) $("#del", m).onclick = async () => {
    if (!confirm(`Delete "${a.name}" and all of its papers and reports? This can't be undone.`)) return;
    await api("/assignments/" + a.id, { method: "DELETE" });
    m.remove(); location.hash = "#/";
  };
  $("#save", m).onclick = async () => {
    const out = {
      ...a, name: $("#an", m).value.trim(), class: $("#ac", m).value.trim(),
      options: { web: $("#ow", m).checked, publications: $("#op", m).checked, repository: $("#or", m).checked, store: $("#os", m).checked },
      filters: { ...(a.filters || {}), excludeQuotes: $("#fq", m).checked, excludeBib: $("#fb", m).checked, excludeCited: $("#fc", m).checked, excludeSmall: +$("#fs", m).value || 0 },
    };
    const saved = await post("/assignments", out);
    m.remove();
    if (!a.id) location.hash = "#/a/" + saved.id; else route();
  };
}

// ---------- Inbox ----------
async function inboxPage(aid) {
  setNav("home");
  const all = await api("/assignments");
  const a = (all || []).find(x => x.id === aid);
  if (!a) { location.hash = "#/"; return; }
  app.innerHTML = `<div class="page">
    <div class="row"><div class="grow"><a href="#/" class="small">← Assignments</a><h1>${esc(a.name)}</h1><p class="sub">${esc(a.class || "")}</p></div>
      <button id="cmp">Compare papers</button><button id="edit">Assignment settings</button></div>
    <div class="card">
      <div class="tabs"><button class="on" data-t="file">Upload files</button><button data-t="paste">Paste text</button></div>
      <div id="t-file">
        <div class="drop" id="drop"><strong>Drop student papers here</strong> or click to choose<br><span class="small">.docx, .pdf, .txt, .rtf or .odt · several files at once for a whole class</span></div>
        <input type="file" id="fi" multiple accept=".docx,.pdf,.txt,.rtf,.odt,.md,.html,.htm" hidden>
        <div class="grid2" style="margin-top:12px"><div><label>Student name (single file; otherwise taken from each file name)</label><input type="text" id="au" placeholder="e.g. Jane Doe"></div><div><label>Paper title (optional)</label><input type="text" id="ti"></div></div>
      </div>
      <div id="t-paste" hidden>
        <div class="grid2"><div><label>Student name</label><input type="text" id="pau"></div><div><label>Paper title</label><input type="text" id="pti"></div></div>
        <div class="spacer"></div><textarea id="ptx" placeholder="Paste the student's text here"></textarea>
        <div class="row" style="margin-top:10px"><div class="grow"></div><button class="primary" id="psub">Check this text</button></div>
      </div>
    </div>
    <div class="card" style="padding:0"><table class="inbox"><thead><tr><th>Student</th><th>Title</th><th>Similarity</th><th>AI writing</th><th class="hide-sm">Flags</th><th class="hide-sm">Words</th><th class="hide-sm">Submitted</th><th></th></tr></thead><tbody id="rows"></tbody></table></div>
  </div>`;
  $("#edit").onclick = () => editAssignment(a);
  $("#cmp").onclick = () => compareModal(a);
  app.querySelectorAll(".tabs button").forEach(b => b.onclick = () => {
    app.querySelectorAll(".tabs button").forEach(x => x.classList.toggle("on", x === b));
    $("#t-file").hidden = b.dataset.t !== "file"; $("#t-paste").hidden = b.dataset.t !== "paste";
  });
  const drop = $("#drop"), fi = $("#fi");
  drop.onclick = () => fi.click();
  drop.ondragover = e => { e.preventDefault(); drop.classList.add("over"); };
  drop.ondragleave = () => drop.classList.remove("over");
  drop.ondrop = e => { e.preventDefault(); drop.classList.remove("over"); upload(e.dataTransfer.files); };
  fi.onchange = () => { upload(fi.files); fi.value = ""; };
  async function upload(files) {
    if (!files || !files.length) return;
    const fd = new FormData();
    fd.append("assignment", aid);
    fd.append("author", $("#au").value); fd.append("title", $("#ti").value);
    for (const f of files) fd.append("files", f);
    drop.innerHTML = `<span class="spin"></span> Uploading ${files.length} file${files.length > 1 ? "s" : ""}…`;
    try {
      const res = await api("/submit", { method: "POST", body: fd });
      const bad = res.filter(r => r.error);
      if (bad.length) alert(bad.map(b => `${b.file}: ${b.error}`).join("\n"));
      else toast(`${res.length} paper${res.length > 1 ? "s" : ""} submitted. Checking now.`);
      $("#au").value = ""; $("#ti").value = "";
    } catch (e) { alert(e.message); }
    drop.innerHTML = `<strong>Drop student papers here</strong> or click to choose<br><span class="small">.docx, .pdf, .txt, .rtf or .odt · several files at once for a whole class</span>`;
    load();
  }
  $("#psub").onclick = async () => {
    const fd = new FormData();
    fd.append("assignment", aid); fd.append("author", $("#pau").value); fd.append("title", $("#pti").value); fd.append("text", $("#ptx").value);
    try { await api("/submit", { method: "POST", body: fd }); $("#ptx").value = ""; toast("Submitted. Checking now."); load(); }
    catch (e) { alert(e.message); }
  };
  async function load() {
    const subs = await api(`/assignments/${aid}/submissions`);
    const tb = $("#rows");
    if (!tb) return;
    if (!subs.length) { tb.innerHTML = `<tr><td colspan="8" class="empty">No papers yet. Upload some above.</td></tr>`; return; }
    tb.innerHTML = "";
    let busy = false;
    for (const s of subs) {
      let sim, ai;
      if (s.status === "done") {
        sim = `<span class="badge ${simClass(s.index)}" data-open="sim">${s.index}%</span>`;
        const at = aiText(s);
        ai = at ? `<span class="badge ai ${at === "*%" ? "star" : ""}" data-open="ai" title="${at === "*%" ? "Between 1% and 19%: low scores are hidden because false positives are more likely" : ""}">${at}</span>` : `<span class="badge none" title="Not enough prose to score">--</span>`;
      } else if (s.status === "error") {
        sim = `<span class="pill err" title="${esc(s.error)}">Error</span>`; ai = "";
      } else {
        busy = true;
        sim = `<span class="small muted"><span class="spin"></span> ${esc(s.progress || "Waiting")}</span>`; ai = "";
      }
      const tr = h(`<table><tr>
        <td class="author">${esc(s.author || "—")}</td>
        <td><a href="#/r/${s.id}">${esc(s.title || s.fileName)}</a></td>
        <td>${sim}</td><td>${ai}</td>
        <td class="hide-sm">${s.flags ? `<span class="pill warn">${s.flags} flag${s.flags > 1 ? "s" : ""}</span>` : ""}</td>
        <td class="hide-sm">${s.words}</td>
        <td class="hide-sm small muted">${esc(fmtDate(s.uploaded))}</td>
        <td style="white-space:nowrap"><button class="link" data-act="recheck" title="Check again">↻</button><a class="btn link" href="/api/submissions/${s.id}/file" title="Download original">⤓</a><button class="link danger" data-act="del" title="Delete">✕</button></td>
      </tr></table>`).querySelector("tr");
      tr.querySelectorAll("[data-open]").forEach(b => b.onclick = () => (location.hash = `#/r/${s.id}${b.dataset.open === "ai" ? "/ai" : ""}`));
      tr.querySelector('[data-act="del"]').onclick = async () => {
        if (!confirm(`Delete ${s.author || s.title}'s paper? It will also be removed from the repository.`)) return;
        await api("/submissions/" + s.id, { method: "DELETE" }); load();
      };
      tr.querySelector('[data-act="recheck"]').onclick = async () => { await post(`/submissions/${s.id}/recheck`, {}); load(); };
      tb.append(tr);
    }
    clearInterval(pollTimer); pollTimer = null;
    if (busy) pollTimer = setInterval(load, 2500);
  }
  load();
}

async function compareModal(a) {
  const m = h(`<div class="modal-bg"><div class="modal"><h2 style="margin-top:0">Papers that overlap each other</h2><p class="muted small">Every pair of papers in “${esc(a.name)}” is compared directly, to spot students sharing work.</p><div id="cbody"><span class="spin"></span> Comparing…</div><div class="row" style="margin-top:12px"><div class="grow"></div><button id="close">Close</button></div></div></div>`);
  document.body.append(m);
  $("#close", m).onclick = () => m.remove();
  m.onclick = e => { if (e.target === m) m.remove(); };
  const r = await api(`/assignments/${a.id}/compare`);
  if (!r.pairs.length) { $("#cbody", m).innerHTML = `<p>No overlapping passages between any of the ${r.papers} papers.</p>`; return; }
  $("#cbody", m).innerHTML = `<table class="plain"><tr><th>Paper A</th><th>Paper B</th><th>Shared words</th><th>% of A</th><th>% of B</th></tr>${r.pairs.map(p =>
    `<tr><td><a href="#/r/${p.A}">${esc(p.aName)}</a></td><td><a href="#/r/${p.B}">${esc(p.bName)}</a></td><td>${p.words}</td><td><span class="badge ${simClass(p.AInB)}">${p.AInB}%</span></td><td><span class="badge ${simClass(p.BInA)}">${p.BInA}%</span></td></tr>`).join("")}</table>`;
  m.querySelectorAll("a").forEach(x => x.addEventListener("click", () => m.remove()));
}

// ---------- Document rendering ----------
// Offsets from the server are UTF-8 byte offsets; convert to JS string indexes.
function byteMap(text) {
  const map = [];
  let b = 0;
  for (let i = 0; i < text.length; i++) {
    const c = text.codePointAt(i);
    const len = c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4;
    for (let k = 0; k < len; k++) map[b + k] = i;
    b += len;
    if (c >= 0x10000) i++;
  }
  map[b] = text.length;
  return b2 => map[Math.min(b2, b)] ?? text.length;
}

// spans: [{start,end (byte), open: html, close: html}] non-overlapping per layer; layers may overlap.
function renderDoc(text, spans) {
  const toIdx = byteMap(text);
  const evs = [];
  spans.forEach((s, i) => {
    const a = toIdx(s.start), b = toIdx(s.end);
    if (b <= a) return;
    evs.push({ pos: a, type: 1, i, s }, { pos: b, type: 0, i, s });
  });
  evs.sort((x, y) => x.pos - y.pos || x.type - y.type || (x.type ? x.s.layer - y.s.layer : y.s.layer - x.s.layer));
  // Build with a stack; reopen spans crossing a closing boundary.
  let out = "", pos = 0;
  const open = [];
  for (const e of evs) {
    if (e.pos > pos) { out += esc(text.slice(pos, e.pos)); pos = e.pos; }
    if (e.type === 1) { out += e.s.open; open.push(e); }
    else {
      const k = open.findIndex(o => o.i === e.i);
      if (k < 0) continue;
      const above = open.splice(k);
      for (let j = above.length - 1; j >= 0; j--) out += above[j].s.close;
      above.shift();
      for (const o of above) { out += o.s.reopen ?? o.s.open; open.push(o); }
    }
  }
  out += esc(text.slice(pos));
  return out;
}

// ---------- Report ----------
async function reportPage(id, view) {
  setNav("");
  let rep = await api(`/submissions/${id}/report`);
  let selSource = null, openSource = null;
  app.innerHTML = `<div class="report with-head">
    <div class="rhead no-print">
      <a href="#/a/${rep.assignment}" class="small">← Inbox</a>
      <div><div class="title">${esc(rep.title || rep.fileName)}</div><div class="who">${esc(rep.author || "Unknown student")} · ${esc(rep.assignmentInfo.name || "")} · ${rep.words} words</div></div>
      <div class="grow"></div>
      <button id="nav-prev" title="Previous paper">‹</button><button id="nav-next" title="Next paper">›</button>
      <button id="edit">Edit details</button>
      <a class="btn" href="/api/submissions/${id}/file">Download original</a>
      <a class="btn primary" href="#/print/${id}" target="_blank">Download PDF report</a>
    </div>
    <div class="docwrap" id="docwrap"><div class="paper" id="paper"></div></div>
    <aside class="side" id="side"></aside>
  </div>`;
  $("#edit").onclick = async () => {
    const author = prompt("Student name", rep.author); if (author === null) return;
    const title = prompt("Paper title", rep.title); if (title === null) return;
    await post(`/submissions/${id}/update`, { author, title }); route();
  };
  const siblings = await api(`/assignments/${rep.assignment}/submissions`);
  const idx = siblings.findIndex(s => s.id === id);
  const goSib = d => { const s = siblings[idx + d]; if (s) location.hash = `#/r/${s.id}${view === "ai" ? "/ai" : ""}`; };
  $("#nav-prev").disabled = idx <= 0; $("#nav-next").disabled = idx < 0 || idx >= siblings.length - 1;
  $("#nav-prev").onclick = () => goSib(-1); $("#nav-next").onclick = () => goSib(1);

  if (rep.status !== "done") {
    $("#paper").textContent = rep.text;
    $("#side").innerHTML = `<div class="sect"><h3>Report</h3>${rep.status === "error" ? `<div class="flag"><b>This paper couldn't be checked.</b><br>${esc(rep.error)}</div><button id="rc">Try again</button>` : `<p><span class="spin"></span> ${esc(rep.progress || "Waiting to be checked")}</p><p class="small muted">The report appears here when the check finishes. Web searches take a minute or two.</p>`}</div>`;
    if ($("#rc")) $("#rc").onclick = async () => { await post(`/submissions/${id}/recheck`, {}); route(); };
    if (rep.status !== "error") pollTimer = setInterval(async () => {
      const r = await api(`/submissions/${id}/report`);
      if (r.status !== rep.status || r.progress !== rep.progress) { if (r.status === "done" || r.status === "error") route(); else { rep = r; $("#side p").innerHTML = `<span class="spin"></span> ${esc(r.progress)}`; } }
    }, 2000);
    return;
  }

  const srcById = {};
  for (const s of rep.sources || []) srcById[s.id] = s;

  function draw() {
    const sim = rep.similarity;
    const paper = $("#paper");
    const spans = [];
    if (view === "sim") {
      for (const x of sim.excluded || []) spans.push({ start: x.start, end: x.end, layer: 0, open: `<span class="excl" title="Excluded by a filter">`, close: `</span>` });
      let lastSrcBlock = "";
      for (const hl of sim.highlights) {
        const c = color(hl.source);
        const key = hl.source + ":" + hl.block;
        const first = key !== lastSrcBlock; lastSrcBlock = key;
        const dim = selSource && selSource !== hl.srcId;
        spans.push({
          start: hl.start, end: hl.end, layer: 1,
          open: `<mark class="hl" data-src="${hl.srcId}" data-block="${hl.block}" style="background:${c}${dim ? "22" : "40"}" title="${esc(hl.group)}">${first ? `<sup class="n" style="background:${c}">${hl.source}</sup>` : ""}`,
          reopen: `<mark class="hl" data-src="${hl.srcId}" data-block="${hl.block}" style="background:${c}${dim ? "22" : "40"}">`,
          close: `</mark>`,
        });
      }
      for (const f of rep.flags) for (const sp of f.spans) spans.push({ start: sp.start, end: sp.end, layer: 2, open: `<span class="flagged-${f.kind}" title="${esc(f.title)}">`, close: `</span>` });
    } else {
      for (const s of rep.aiReport.sentences || []) if (s.flagged) spans.push({ start: s.start, end: s.end, layer: 1, open: `<mark class="ai" title="Likely AI-generated (score ${Math.round(s.score * 100)})">`, close: `</mark>` });
    }
    paper.innerHTML = renderDoc(rep.text, spans);
    paper.querySelectorAll("mark.hl").forEach(m => m.onclick = () => { selSource = +m.dataset.src; openSource = selSource; draw(); scrollSide(selSource); });
    drawSide();
  }

  function scrollSide(sid) {
    const el = $(`#side [data-sid="${sid}"]`);
    if (el) el.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }

  function drawSide() {
    const side = $("#side");
    const sim = rep.similarity;
    const aiT = aiText(rep);
    side.innerHTML = `<div class="viewtabs"><button data-v="sim" class="${view === "sim" ? "on" : ""}">Similarity ${sim.index}%</button><button data-v="ai" class="aitab ${view === "ai" ? "on" : ""}">AI writing ${aiT ?? "--"}</button></div><div id="sidebody"></div>`;
    side.querySelectorAll(".viewtabs button").forEach(b => b.onclick = () => { location.hash = `#/r/${id}${b.dataset.v === "ai" ? "/ai" : ""}`; });
    const body = $("#sidebody");
    if (view === "ai") { drawAISide(body); return; }

    const notes = (rep.notes || []).map(n => `<div class="notice">${esc(n)}</div>`).join("");
    const flags = rep.flags.length ? `<div class="sect"><h3>Integrity flags</h3>${rep.flags.map(f => `<div class="flag"><b>${esc(f.title)}</b><br>${esc(f.detail)}</div>`).join("")}</div>` : "";
    const f = rep.filters;
    const nEx = (f.excludedSources || []).length + (f.excludedMatches || []).length;
    body.innerHTML = `
      <div class="sect"><div class="bigscore"><span class="num">${sim.index}%</span><span class="lbl">Overall similarity</span></div>
        <div class="small muted">${sim.matchedWords} of ${sim.totalWords} words match other sources${nEx ? ` · ${nEx} exclusion${nEx > 1 ? "s" : ""}` : ""}</div>${notes}</div>
      <div class="sect"><h3>Top sources by type</h3><div class="bars">
        ${[["Internet", "Internet sources"], ["Publication", "Publications"], ["Student Paper", "Submitted works"]].map(([k, l]) => `<div class="bar"><span>${l}</span><div class="track"><div class="fill" style="width:${sim.byType[k] || 0}%"></div></div><b>${sim.byType[k] || 0}%</b></div>`).join("")}
      </div></div>
      <div class="sect groups"><h3>Match groups</h3>${sim.groups.map(g => `<div class="g"><span class="sw" style="background:${GROUP_COLORS[g.name]}"></span>${esc(g.name)}<span class="n">${g.count} · ${g.percent}%</span></div>`).join("")}
        <div class="small muted" style="margin-top:6px">Whether each match is in quotation marks and has an in-text citation nearby.</div></div>
      ${flags}
      <div class="sect"><h3>Filters</h3>
        <label class="check"><input type="checkbox" id="xq" ${f.excludeQuotes ? "checked" : ""}> Exclude quotes</label>
        <label class="check"><input type="checkbox" id="xb" ${f.excludeBib ? "checked" : ""}> Exclude bibliography${sim.bibStart < 0 ? ` <span class="small muted">(none found)</span>` : ""}</label>
        <label class="check"><input type="checkbox" id="xc" ${f.excludeCited ? "checked" : ""}> Exclude cited text</label>
        <div class="switch"><span>Exclude matches under</span><span><input type="number" id="xs" min="0" max="200" value="${f.excludeSmall || 0}"> words</span></div>
        ${nEx ? `<button class="link" id="xall">Restore all excluded sources and matches</button>` : ""}
      </div>
      <div class="sect" style="padding-bottom:6px"><h3>Match overview</h3>${sim.sources.filter(s => s.rank).length ? "" : `<p class="muted">No matches found.</p>`}</div>
      <div id="srcs"></div>`;
    const srcs = $("#srcs");
    for (const s of sim.sources) {
      const full = srcById[s.id] || {};
      const rankBox = s.rank ? `<div class="rank" style="background:${color(s.rank)}">${s.rank}</div>` : `<div class="rank" style="background:#cbd5e1">–</div>`;
      const where = full.type === "Student Paper" ? "Student paper in your repository" : (s.url ? hostOf(s.url) : "");
      const row = h(`<div class="src ${selSource === s.id ? "sel" : ""} ${s.rank ? "" : "dim"}" data-sid="${s.id}">${rankBox}
        <div><div class="type">${esc(s.type === "Student Paper" ? "Submitted works" : s.type)}</div><div class="t">${esc(s.title || s.url)}</div><div class="u">${esc(where)}</div>
        <div class="small muted">${s.excluded ? "Excluded" : s.rank ? `${s.blocks} matching passage${s.blocks === 1 ? "" : "s"} · ${s.words} words` : `${s.totalWords} words, all also in a higher source`}</div></div>
        <div class="p">${s.excluded ? "" : s.rank ? pctText(s.percent) : ""}</div></div>`);
      row.onclick = () => { openSource = openSource === s.id ? null : s.id; selSource = openSource; draw(); if (openSource) scrollSide(openSource); };
      srcs.append(row);
      if (openSource === s.id) srcs.append(sourceDetail(s, full));
    }
    const upd = async patch => {
      rep = { ...rep, ...(await post(`/submissions/${id}/filters`, { ...rep.filters, ...patch })) };
      draw();
    };
    $("#xq").onchange = e => upd({ excludeQuotes: e.target.checked });
    $("#xb").onchange = e => upd({ excludeBib: e.target.checked });
    $("#xc").onchange = e => upd({ excludeCited: e.target.checked });
    $("#xs").onchange = e => upd({ excludeSmall: Math.max(0, +e.target.value || 0) });
    if ($("#xall")) $("#xall").onclick = () => upd({ excludedSources: [], excludedMatches: [] });

    function sourceDetail(s, full) {
      const d = h(`<div class="srcdetail"></div>`);
      const exS = (rep.filters.excludedSources || []).includes(s.id);
      const link = full.url ? `<a href="${esc(full.url)}" target="_blank" rel="noopener noreferrer">Open source ↗</a>` : (full.ref ? `<a href="#/r/${full.ref}">Open that paper's report</a>` : "");
      d.innerHTML = `<div class="row small">${link}<div class="grow"></div><button class="link" id="exs">${exS ? "Include source" : "Exclude source"}</button></div>`;
      $("#exs", d).onclick = e => {
        e.stopPropagation();
        const cur = new Set(rep.filters.excludedSources || []);
        exS ? cur.delete(s.id) : cur.add(s.id);
        upd({ excludedSources: [...cur] });
      };
      const shown = new Set(rep.similarity.highlights.filter(x => x.srcId === s.id).map(x => x.block));
      (full.blocks || []).forEach((b, bi) => {
        const key = `${s.id}:${bi}`;
        const exM = (rep.filters.excludedMatches || []).includes(key);
        if (!shown.has(bi) && !exM) return;
        const toIdx = byteMap(b.excerpt);
        const a0 = toIdx(b.exStart), a1 = toIdx(b.exEnd);
        const ex = h(`<div class="excerpt"><div class="hd"><span>Source text${exM ? " (excluded)" : ""}</span><span><button class="link" data-k="${key}">${exM ? "Include match" : "Exclude match"}</button> <button class="link" data-go="${bi}">Show in paper</button></span></div>…${esc(b.excerpt.slice(0, a0))}<mark>${esc(b.excerpt.slice(a0, a1))}</mark>${esc(b.excerpt.slice(a1))}…</div>`);
        $("[data-k]", ex).onclick = e => {
          e.stopPropagation();
          const cur = new Set(rep.filters.excludedMatches || []);
          exM ? cur.delete(key) : cur.add(key);
          upd({ excludedMatches: [...cur] });
        };
        $("[data-go]", ex).onclick = e => {
          e.stopPropagation();
          const m = $(`#paper mark.hl[data-src="${s.id}"][data-block="${bi}"]`);
          if (m) { m.scrollIntoView({ block: "center", behavior: "smooth" }); m.classList.add("sel"); setTimeout(() => m.classList.remove("sel"), 1600); }
        };
        d.append(ex);
      });
      return d;
    }
  }

  function drawAISide(body) {
    const ai = rep.aiReport;
    let score, desc;
    if (!ai.available) { score = "--"; desc = esc(ai.reason || "Not available"); }
    else if (ai.percent === 0) { score = "0%"; desc = "No text was detected as likely AI-generated."; }
    else if (ai.percent < 20) { score = "*%"; desc = `Between 1% and 19% of the qualifying text was flagged. Like Turnitin, scores this low are hidden, because false positives are more likely in this range. The flagged sentences are still highlighted.`; }
    else { score = ai.percent + "%"; desc = `${ai.flaggedWords} of ${ai.qualifyingWords} words of qualifying text are likely AI-generated, or AI-generated and then paraphrased.`; }
    body.innerHTML = `<div class="sect aiscore"><div class="bigscore"><span class="num">${score}</span><span class="lbl">detected as AI</span></div><p class="small">${desc}</p></div>
      <div class="sect"><h3>What was scored</h3><div class="kv"><div>Qualifying text</div><div>${ai.qualifyingWords || 0} words</div><div>Flagged</div><div>${ai.flaggedWords || 0} words</div><div>Total words</div><div>${rep.words}</div></div>
      <p class="small muted">Only prose is scored: full sentences in paragraphs. Headings, bullet points, tables, poetry, code and the bibliography are skipped. At least 300 words of prose are needed.</p>
      <p class="small"><span style="background:var(--ai-bg);border-bottom:2px solid var(--ai);padding:0 4px">Highlighted</span> text is likely AI-generated.</p></div>
      <div class="sect"><div class="info"><b>Use this score with care.</b> AI detection can be wrong, both ways, and it should never be the only basis for action against a student. Talk to the student, look at drafts and version history, and compare with their earlier work. <a href="#/about">How accurate is it?</a></div></div>`;
  }

  draw();
}

// ---------- Printable report ----------
async function printPage(id) {
  const rep = await api(`/submissions/${id}/report`);
  document.querySelector(".topbar").style.display = "none";
  const sim = rep.similarity, ai = rep.aiReport;
  const aiT = aiText(rep) ?? "--";
  const srcRows = sim.sources.filter(s => s.rank).map(s => `<tr><td><span style="display:inline-block;width:22px;height:22px;border-radius:5px;background:${color(s.rank)};color:#fff;text-align:center;font-weight:700;line-height:22px;-webkit-print-color-adjust:exact;print-color-adjust:exact">${s.rank}</span></td><td><b>${esc(s.title || s.url)}</b><br><span class="muted">${esc(s.type === "Student Paper" ? "Submitted works" : s.type)}${s.url ? " · " + esc(s.url) : ""}</span></td><td style="text-align:right"><b>${pctText(s.percent)}</b></td></tr>`).join("");
  const spans = [];
  for (const x of sim.excluded || []) spans.push({ start: x.start, end: x.end, layer: 0, open: `<span class="excl">`, close: `</span>` });
  let last = "";
  for (const hl of sim.highlights) {
    const c = color(hl.source), key = hl.source + ":" + hl.block, first = key !== last; last = key;
    spans.push({ start: hl.start, end: hl.end, layer: 1, open: `<mark class="hl" style="background:${c}40">${first ? `<sup class="n" style="background:${c}">${hl.source}</sup>` : ""}`, reopen: `<mark class="hl" style="background:${c}40">`, close: `</mark>` });
  }
  const aiSpans = (ai.sentences || []).filter(s => s.flagged).map(s => ({ start: s.start, end: s.end, layer: 1, open: `<mark class="ai">`, close: `</mark>` }));
  const f = rep.filters;
  const filt = [f.excludeQuotes && "quotes", f.excludeBib && "bibliography", f.excludeCited && "cited text", f.excludeSmall && `matches under ${f.excludeSmall} words`].filter(Boolean);
  app.innerHTML = `<div class="print">
    <div class="no-print row" style="margin-bottom:16px"><div class="grow muted small">Use your browser's print dialog and choose “Save as PDF”.</div><button class="primary" onclick="print()">Print / Save as PDF</button></div>
    <div class="cover"><div class="muted small">${esc(rep.institution || "")} · OriginCheck report</div><h1>${esc(rep.title || rep.fileName)}</h1><div>${esc(rep.author || "Unknown student")}</div></div>
    <div class="kv"><div>Assignment</div><div>${esc(rep.assignmentInfo.name || "")}${rep.assignmentInfo.class ? " · " + esc(rep.assignmentInfo.class) : ""}</div>
      <div>File name</div><div>${esc(rep.fileName)}</div><div>Submission ID</div><div>${esc(rep.id)}</div>
      <div>Submitted</div><div>${esc(fmtDate(rep.uploaded))}</div><div>Checked</div><div>${esc(fmtDate(rep.checked))}</div>
      <div>Words / characters</div><div>${rep.words} / ${rep.chars}</div><div>Filters</div><div>${filt.length ? "Excluded " + filt.join(", ") : "None"}</div></div>
    <div class="scorebox"><div><div class="muted small">Overall similarity</div><div class="num">${sim.index}%</div><div class="small">Internet ${sim.byType.Internet || 0}% · Publications ${sim.byType.Publication || 0}% · Submitted works ${sim.byType["Student Paper"] || 0}%</div></div>
      <div><div class="muted small">AI writing</div><div class="num" style="color:#0e7490">${aiT}</div><div class="small">${ai.available ? `${ai.flaggedWords} of ${ai.qualifyingWords} qualifying words` : esc(ai.reason || "")}</div></div></div>
    ${(rep.notes || []).map(n => `<div class="notice">${esc(n)}</div>`).join("")}
    ${rep.flags.map(x => `<div class="flag"><b>Integrity flag: ${esc(x.title)}</b><br>${esc(x.detail)}</div>`).join("")}
    <h2>Match groups</h2><table class="plain">${sim.groups.map(g => `<tr><td>${esc(g.name)}</td><td>${g.count} passages</td><td>${g.percent}%</td></tr>`).join("")}</table>
    <h2>Top sources</h2>${srcRows ? `<table class="plain">${srcRows}</table>` : `<p>No matching sources.</p>`}
    <div class="pagebreak"></div><h2>Similarity: highlighted document</h2><div class="paper">${renderDoc(rep.text, spans)}</div>
    ${ai.available ? `<div class="pagebreak"></div><h2>AI writing: ${aiT} detected as AI</h2><p class="small muted">Highlighted sentences are likely AI-generated. AI detection can be wrong and should not be the only basis for action against a student.</p><div class="paper">${renderDoc(rep.text, aiSpans)}</div>` : ""}
  </div>`;
  setTimeout(() => window.print(), 600);
}

// ---------- Settings ----------
async function settingsPage() {
  setNav("settings");
  const s = await api("/settings");
  const info = await api("/info");
  app.innerHTML = `<div class="page"><h1>Settings</h1><p class="sub">Your data is stored on this computer in <code>${esc(info.dataDir)}</code>. ${info.repository} paper${info.repository === 1 ? " is" : "s are"} in your repository.</p>
    <div class="card"><h2>General</h2><label>School or institution name (shown on PDF reports)</label><input type="text" id="inst" value="${esc(s.institutionName)}"></div>
    <div class="card"><h2>Web search</h2>
      <p class="small">OriginCheck searches the web without any key, using DuckDuckGo and Bing, plus Wikipedia and OpenAlex. Keyless search can be slowed down or blocked if you check many papers at once. For dependable results, add a free search API key. <b>Brave Search</b> is the easiest: sign up at <a href="https://brave.com/search/api/" target="_blank" rel="noopener">brave.com/search/api</a> and choose the free plan.</p>
      <div class="grid2">
        <div><label>Brave Search API key</label><input type="password" id="brave" value="${esc(s.braveKey)}" autocomplete="off"></div>
        <div><label>Bing Web Search API key (optional)</label><input type="password" id="bing" value="${esc(s.bingKey)}" autocomplete="off"></div>
        <div><label>Google Custom Search API key (optional)</label><input type="password" id="gkey" value="${esc(s.googleKey)}" autocomplete="off"></div>
        <div><label>Google search engine ID (cx)</label><input type="text" id="gcx" value="${esc(s.googleCx)}"></div>
        <div><label>Searches per paper</label><input type="number" id="mq" min="5" max="200" value="${s.maxQueries}"></div>
        <div><label>Web pages to compare per paper</label><input type="number" id="mp" min="5" max="150" value="${s.maxPages}"></div>
      </div>
      <div class="row" style="margin-top:14px"><button id="test">Test web search</button><div class="grow"></div></div>
      <div id="testres"></div>
    </div>
    <div class="row"><div class="grow"></div><button class="primary" id="save">Save settings</button></div></div>`;
  const read = () => ({ institutionName: $("#inst").value, braveKey: $("#brave").value.trim(), bingKey: $("#bing").value.trim(), googleKey: $("#gkey").value.trim(), googleCx: $("#gcx").value.trim(), maxQueries: +$("#mq").value, maxPages: +$("#mp").value });
  $("#save").onclick = async () => { await post("/settings", read()); toast("Settings saved"); };
  $("#test").onclick = async () => {
    $("#testres").innerHTML = `<p><span class="spin"></span> Testing…</p>`;
    const r = await post("/settings/test", read());
    $("#testres").innerHTML = `<table class="plain">${r.map(x => `<tr><td>${esc(x.engine)}</td><td>${x.ok ? "✅ Working" : "❌ Not working"}</td><td class="small muted">${esc(x.detail)}</td></tr>`).join("")}</table>`;
  };
}

// ---------- About ----------
async function aboutPage() {
  setNav("about");
  const info = infoCache || (infoCache = await api("/info"));
  const acc = info.accuracy && info.accuracy.groups ? info.accuracy : null;
  const rows = acc ? acc.groups.map(g => `<tr><td>${esc(g.name)}</td><td>${esc(g.kind)}</td><td>${g.docs}</td><td><b>${g.rate}%</b></td></tr>`).join("") : "";
  app.innerHTML = `<div class="page" style="max-width:900px">
    <h1>How OriginCheck works</h1><p class="sub">OriginCheck is modelled on Turnitin's Similarity Report and AI Writing Report.</p>
    <div class="card"><h2>Similarity report</h2>
      <p>Each paper is broken into sentences. Distinctive sentences are searched on the web, and Wikipedia and the OpenAlex database of published papers are searched by topic. The pages found are downloaded and compared word by word with the paper, together with every paper stored in your repository. Passages of 7 or more words that match, allowing for small edits such as a changed or missing word, are highlighted.</p>
      <p><b>Overall similarity</b> is the share of the paper's words that match any source. Each matching word is credited to one source only, the one with the most matches, so the source percentages add up to the overall score, as in Turnitin's Match Overview. The breakdown by source type counts each type separately, so those can add up to more.</p>
      <p><b>Match groups</b> sort every match by whether it is in quotation marks and whether an in-text citation such as (Smith, 2020) or [3] is in the same sentence: Not Cited or Quoted, Missing Quotations, Missing Citation, and Cited and Quoted.</p>
      <p><b>Filters</b> can exclude quoted text, the bibliography, cited text, short matches, whole sources or single matches. <b>Integrity flags</b> warn about letters swapped for look-alikes from other alphabets and about hidden (white or tiny) text in Word files.</p>
      <p><b>What it can't do:</b> Turnitin also compares with its private database of over a billion student papers and with subscription journals. Those aren't available to anyone else, so OriginCheck can only find copying from the open web, open scholarly abstracts, and papers you have checked yourself. The more of your classes' papers you store, the more useful the repository becomes.</p>
    </div>
    <div class="card"><h2>AI writing report</h2>
      <p>The detector is a statistical model trained on thousands of essays, news articles and stories written by people and by AI models (ChatGPT, Claude and others), including essays by English learners. It looks at word choice, how predictable the vocabulary is, sentence rhythm, punctuation and phrasing habits. Passages of about 150 words are scored in overlapping windows, and each sentence gets the average score of the windows it sits in.</p>
      <p>Like Turnitin, only prose counts, at least 300 words are needed, runs of at least two sentences must be flagged, and scores from 1% to 19% are shown as <b>*%</b> because false alarms are likelier there.</p>
      ${acc ? `<h3>Measured accuracy</h3><p class="small">${esc(acc.summary)}</p><table class="plain"><tr><th>Test set (never used in training)</th><th>Written by</th><th>Papers</th><th>${esc(acc.metric)}</th></tr>${rows}</table>` : ""}
      <div class="info" style="margin-top:14px"><b>Honest limits.</b> No AI detector is reliable enough to prove misconduct, including Turnitin's, which also says its score “should not be used as the sole basis for adverse actions”. This one is smaller than Turnitin's and runs entirely on your computer. It catches most unedited AI essays but misses many AI essays that were heavily edited or reworded, and it occasionally flags human writing. Treat a high score as a reason to talk with the student, not as proof.</div>
    </div></div>`;
}

route();
