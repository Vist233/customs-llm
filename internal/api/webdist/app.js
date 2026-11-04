"use strict";
// customs-llm web UI: no build step, no dependencies.
const TOKEN = document.querySelector('meta[name="customs-token"]').content;
const $app = document.getElementById("app");
const STATE_LABEL = { ok: "成功", http_error: "HTTP 错误", transport_error: "连接失败", incomplete: "流未结束", running: "进行中" };
const TIER_LABEL = { wire: "亲眼所见", reported: "网关自述", inferred: "推断" };

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const ms = (n) => (n == null ? "-" : n >= 10000 ? (n / 1000).toFixed(1) + "s" : n >= 1000 ? (n / 1000).toFixed(2) + "s" : n + "ms");
const tm = (iso) => new Date(iso).toLocaleTimeString("zh-CN", { hour12: false });
const tok = (n) => (n == null || n < 0 ? "-" : String(n));
const pretty = (t) => { try { return JSON.stringify(JSON.parse(t), null, 2); } catch { return t; } };

async function api(path, opts) {
  const r = await fetch(path, opts);
  if (!r.ok) throw new Error(path + " → " + r.status + " " + (await r.text()).slice(0, 200));
  return r;
}
const getJSON = async (p) => (await api(p)).json();
const getText = async (p) => (await api(p)).text();

const badge = (state) => `<span class="badge b-${esc(state)}">${esc(STATE_LABEL[state] || state)}</span>`;
const tierBadge = (t) => `<span class="tier ${esc(t)}" title="证据来源">${esc(TIER_LABEL[t] || t)}</span>`;

// ---------- layout ----------
function shell(active, body) {
  $app.innerHTML = `
  <header class="top"><b>customs-llm</b>
    <nav>
      <a href="#/" class="${active === "traces" ? "on" : ""}">请求</a>
      <a href="#/checks" class="${active === "checks" ? "on" : ""}">检查</a>
      <a href="#/sources" class="${active === "sources" ? "on" : ""}">来源</a>
    </nav></header>
  <div class="banner">本地记录含密钥与对话原文,仅保存在本机。命令行与导出默认脱敏;这里显示的是原始数据。</div>
  <main>${body}</main>`;
}

// ---------- traces list ----------
let listFilter = { state: "", q: "" };
async function viewTraces() {
  shell("traces", `<div class="bar">
    ${[["", "全部"], ["failed", "失败"], ["running", "进行中"]].map(([v, l]) => `<button data-f="${v}" class="${listFilter.state === v ? "on" : ""}">${l}</button>`).join("")}
    <input id="q" placeholder="搜索模型 / 路径 / 客户端 / 目标" value="${esc(listFilter.q)}" size="36">
    <span class="muted" id="count"></span></div><div id="list"></div>`);
  document.querySelectorAll("[data-f]").forEach((b) => (b.onclick = () => { listFilter.state = b.dataset.f; viewTraces(); }));
  document.getElementById("q").oninput = debounce((e) => { listFilter.q = e.target.value; refreshList(); }, 250);
  await refreshList();
}

async function refreshList() {
  const el = document.getElementById("list");
  if (!el) return;
  const p = new URLSearchParams({ limit: "200" });
  if (listFilter.state) p.set("state", listFilter.state);
  if (listFilter.q) p.set("q", listFilter.q);
  const list = await getJSON("/api/traces?" + p);
  document.getElementById("count").textContent = list.length + " 条";
  if (!list.length) {
    el.innerHTML = `<div class="empty">还没有记录。<br>让客户端经过中转:<code class="mono">customs run --target NAME -- 你的命令</code>,或到「来源」页查看怎么接入。</div>`;
    return;
  }
  el.innerHTML = `<table><thead><tr><th>时间</th><th>状态</th><th>请求</th><th>目标</th><th>模型</th><th class="right">耗时 / 首字节</th>
    <th class="right">in / out / cache</th><th>finish</th><th>标记</th></tr></thead><tbody>${list.map((t) => `
    <tr class="link" data-id="${esc(t.id)}"><td class="mono">${tm(t.started)}</td><td>${badge(t.state)}${t.status ? ` <span class="muted mono">${t.status}</span>` : ""}</td>
    <td class="mono">${esc(t.method)} ${esc(t.path)}${t.streaming ? ' <span class="muted">流</span>' : ""}</td><td>${esc(t.target)}</td>
    <td class="mono">${esc(t.model)}</td><td class="right mono">${t.state === "running" ? "…" : ms(t.ms)} / ${t.ttfb_ms ? ms(t.ttfb_ms) : "-"}</td>
    <td class="right mono">${tok(t.usage.in)} / ${tok(t.usage.out)} / ${tok(t.usage.cache_read)}</td><td class="mono">${esc(t.finish || "-")}</td>
    <td>${(t.flags || []).map((f) => `<span class="badge b-warn">${esc(f)}</span>`).join(" ")}</td></tr>`).join("")}</tbody></table>`;
  el.querySelectorAll("tr.link").forEach((tr) => (tr.onclick = () => (location.hash = "#/trace/" + tr.dataset.id)));
}

// ---------- trace detail ----------
let detailTab = "reconcile";
async function viewTrace(id) {
  let data;
  try { data = await getJSON("/api/traces/" + id); } catch (e) { shell("traces", `<div class="empty">${esc(e.message)}</div>`); return; }
  const t = data.trace, hops = data.hops;
  const hop = hops.find((h) => h.tier === "wire") || hops[0];
  shell("traces", `<p><a href="#/">← 返回列表</a></p>
    <div class="card"><div class="bar" style="margin:0">${badge(t.state)}
      <b class="mono">${esc(t.method)} ${esc(t.path)}</b><span class="muted">${esc(t.client)} → ${esc(t.target)}</span>
      <span class="mono">${esc(t.model)}</span><span class="muted">${t.state === "running" ? "进行中…" : ms(t.ms)}</span></div>
      <p style="margin:8px 0 0">${summarySentence(t, hop)}</p></div>
    <div class="card"><h3>耗时</h3><div id="wf"></div></div>
    <div class="tabs">${[["reconcile", "对账表"], ["chat", "对话"], ["stream", "流"], ["error", "错误"], ["raw", "原始"]].map(([k, l]) => `<button data-t="${k}" class="${detailTab === k ? "on" : ""}">${l}</button>`).join("")}</div>
    <div id="tab"></div>`);
  document.querySelectorAll("[data-t]").forEach((b) => (b.onclick = () => { detailTab = b.dataset.t; viewTrace(id); }));
  const events = hop && hop.streaming ? await getJSON(`/api/traces/${id}/hops/${hop.id}/events`) : [];
  document.getElementById("wf").innerHTML = waterfall(t, hop, events);
  const tab = document.getElementById("tab");
  try {
    if (detailTab === "reconcile") await tabReconcile(tab, id, hops);
    else if (detailTab === "chat") await tabChat(tab, id, hop, events);
    else if (detailTab === "stream") tabStream(tab, hop, events);
    else if (detailTab === "error") await tabError(tab, id, hop);
    else await tabRaw(tab, id, hops);
  } catch (e) { tab.innerHTML = `<div class="empty">${esc(e.message)}</div>`; }
}

function summarySentence(t, h) {
  const parts = [];
  if (t.state === "running") return "请求还在进行中。";
  if (h && h.error) parts.push(`<b class="stall">${esc(h.error)}</b>`);
  if (t.status) parts.push(`上游返回 HTTP ${t.status}`);
  if (t.state === "http_error") parts.push("上游的原始错误体见「错误」标签,它可能和客户端最终显示的文案不同");
  if (t.state === "incomplete") parts.push("流式响应没有收到结束标记,可能被截断");
  if (t.streaming && t.ttfb_ms) parts.push(`首字节 ${ms(t.ttfb_ms)},总计 ${ms(t.ms)}`);
  if (t.usage.in >= 0) parts.push(`输入 ${t.usage.in} / 输出 ${t.usage.out} token` + (t.usage.cache_read > 0 ? `(缓存读取 ${t.usage.cache_read})` : t.usage.cache_read === 0 ? "(缓存读取 0)" : ""));
  return parts.join(";") + "。";
}

function waterfall(t, h, events) {
  if (!h || !h.ended || h.ended.startsWith("0001")) return `<div class="muted">进行中…</div>`;
  const t0 = new Date(h.started).getTime(), end = new Date(h.ended).getTime() - t0;
  const fb = h.first_byte && !h.first_byte.startsWith("0001") ? new Date(h.first_byte).getTime() - t0 : null;
  const W = 900, L = 90, scale = (v) => L + (v / Math.max(end, 1)) * (W - L - 20);
  let svg = `<svg viewBox="0 0 ${W} 86" width="100%" role="img" aria-label="耗时瀑布图">`;
  svg += `<text x="0" y="22">等待上游</text><rect x="${L}" y="10" width="${Math.max(scale(fb ?? end) - L, 2)}" height="16" rx="3" fill="var(--warn)" opacity=".8"/>`;
  if (fb != null) svg += `<text x="0" y="50">接收响应</text><rect x="${scale(fb)}" y="38" width="${Math.max(scale(end) - scale(fb), 2)}" height="16" rx="3" fill="var(--ok)" opacity=".8"/>`;
  let stalls = 0, prev = fb ?? 0;
  const limit = Math.max(2000, end * 0.25);
  events.forEach((e) => {
    const x = scale(e.offset_us / 1000);
    svg += `<line x1="${x}" x2="${x}" y1="38" y2="54" stroke="#fff" opacity=".7"/>`;
    if (e.offset_us / 1000 - prev > limit) { stalls++; svg += `<rect x="${scale(prev)}" y="38" width="${x - scale(prev)}" height="16" fill="var(--bad)" opacity=".55"/>`; }
    prev = e.offset_us / 1000;
  });
  svg += `<text x="${L}" y="78">0</text>`;
  if (fb != null) svg += `<text x="${scale(fb) - 10}" y="78">首字节 ${ms(Math.round(fb))}</text>`;
  svg += `<text x="${W - 70}" y="78">${ms(Math.round(end))}</text></svg>`;
  return svg + (stalls ? `<div class="stall">流中有 ${stalls} 处停顿超过 ${ms(Math.round(limit))}(红色)</div>` : "");
}

// ----- reconcile tab -----
let onlyDiff = true;
async function tabReconcile(el, id, hops) {
  const r = await getJSON(`/api/traces/${id}/reconcile`);
  const multi = r.columns.length > 1;
  const rows = multi && onlyDiff ? r.rows.filter((x) => x.diff !== "same") : r.rows;
  const hidden = r.rows.length - rows.length;
  let html = `<div class="bar">${multi ? `<button id="od" class="${onlyDiff ? "on" : ""}">只看有差异</button>` : ""}
    <span class="hint">${multi ? `${hidden} 项一致已折叠。` : "只有一跳记录,先显示这一跳读到的内容。要对比出站字节,请启动测试模型(customs probe)或接入网关日志(来源页)。"}
    没有数据的格子显示「未知」,不猜。</span></div>`;
  if (!rows.length) { el.innerHTML = html + `<div class="empty">没有可显示的行。</div>`; bindOd(id); return; }
  html += `<table><thead><tr><th>项</th>${r.columns.map((c) => `<th>${esc(c.name)}${tierBadge(c.tier)}<div class="muted">${esc(c.protocol || "")}</div></th>`).join("")}</tr></thead><tbody>`;
  let group = "";
  for (const row of rows) {
    if (row.group !== group) { group = row.group; html += `<tr class="group"><td colspan="${r.columns.length + 1}">${esc(group)}</td></tr>`; }
    const issues = (row.issues || []).length ? `<span class="issue" title="${esc("测试集中的相关 issue:\n" + row.issues.join("\n"))}">已知问题 ${row.issues.length}</span>` : "";
    html += `<tr class="d-${esc(row.diff)}"><td>${esc(row.label)}${issues}</td>${row.cells.map((c) => `<td class="v mono">${c.value === "" ? '<span class="muted">未知</span>' : esc(c.value)}</td>`).join("")}</tr>`;
  }
  el.innerHTML = html + "</tbody></table>";
  bindOd(id);
}
function bindOd(id) { const b = document.getElementById("od"); if (b) b.onclick = () => { onlyDiff = !onlyDiff; viewTrace(id); }; }

// ----- conversation tab -----
function blockText(c) {
  if (typeof c === "string") return esc(c);
  if (!Array.isArray(c)) return c == null ? "" : `<pre>${esc(JSON.stringify(c, null, 2))}</pre>`;
  return c.map((b) => {
    if (typeof b === "string") return esc(b);
    const ty = b.type || "";
    if (ty === "text" || ty === "input_text" || ty === "output_text") return `<div style="white-space:pre-wrap">${esc(b.text)}</div>`;
    if (ty === "image_url" || ty === "input_image" || ty === "image") {
      const u = (b.image_url && (b.image_url.url || b.image_url)) || b.image_url || (b.source && b.source.type === "base64" ? `data:${b.source.media_type};base64,${b.source.data}` : b.source && b.source.url) || "";
      return typeof u === "string" && u.startsWith("data:") ? `<img src="${esc(u)}" alt="图片">` : `<span class="badge">图片 ${esc(String(u).slice(0, 60))}</span>`;
    }
    if (ty === "tool_use" || ty === "function_call") return `<details open><summary>🔧 ${esc(b.name)}</summary><pre>${esc(typeof b.input === "object" ? JSON.stringify(b.input, null, 2) : b.arguments || "")}</pre></details>`;
    if (ty === "tool_result" || ty === "function_call_output") return `<details><summary>↩ 工具结果${b.is_error ? ' <span class="badge b-bad">is_error</span>' : ""}</summary><div style="white-space:pre-wrap">${blockText(b.content ?? b.output)}</div></details>`;
    if (ty === "thinking" || ty === "reasoning") return `<details><summary>💭 推理</summary><div style="white-space:pre-wrap">${esc(b.thinking || (b.summary || []).map((s) => s.text).join("\n"))}</div></details>`;
    return `<details><summary>${esc(ty || "块")}</summary><pre>${esc(JSON.stringify(b, null, 2))}</pre></details>`;
  }).join("");
}

function msgBox(role, inner, cls) { return `<div class="msg ${esc(cls || role)}"><div class="role">${esc(role)}</div>${inner}</div>`; }

function conversation(protocol, req) {
  const out = [];
  if (protocol === "anthropic_messages") {
    if (req.system) out.push(msgBox("system", blockText(req.system)));
    for (const m of req.messages || []) out.push(msgBox(m.role, blockText(m.content)));
  } else if (protocol === "openai_responses") {
    if (req.instructions) out.push(msgBox("system", esc(req.instructions)));
    if (typeof req.input === "string") out.push(msgBox("user", esc(req.input)));
    for (const it of req.input || []) {
      if (typeof it === "string") out.push(msgBox("user", esc(it)));
      else if (it.type === "function_call" || it.type === "function_call_output" || it.type === "reasoning") out.push(msgBox("tool", blockText([it])));
      else out.push(msgBox(it.role || "message", blockText(it.content)));
    }
  } else {
    for (const m of req.messages || []) {
      let inner = blockText(m.content);
      for (const c of m.tool_calls || []) inner += blockText([{ type: "function_call", name: c.function && c.function.name, arguments: c.function && c.function.arguments }]);
      if (m.role === "tool") inner = blockText([{ type: "tool_result", content: m.content }]);
      out.push(msgBox(m.role, inner));
    }
  }
  return out.join("");
}

function replyText(protocol, hop, events, body) {
  try {
    if (events.length) {
      let txt = "";
      for (const e of events) {
        let j; try { j = JSON.parse(e.data); } catch { continue; }
        if (protocol === "anthropic_messages") { if (j.delta && j.delta.type === "text_delta") txt += j.delta.text; }
        else if (protocol === "openai_responses") { if (j.type === "response.output_text.delta") txt += j.delta; }
        else if (j.choices && j.choices[0] && j.choices[0].delta && j.choices[0].delta.content) txt += j.choices[0].delta.content;
      }
      return txt;
    }
    const j = JSON.parse(body);
    if (protocol === "anthropic_messages") return (j.content || []).filter((b) => b.type === "text").map((b) => b.text).join("");
    if (protocol === "openai_responses") return (j.output || []).flatMap((o) => o.content || []).map((c) => c.text || "").join("");
    return (j.choices && j.choices[0] && j.choices[0].message && j.choices[0].message.content) || "";
  } catch { return ""; }
}

async function tabChat(el, id, hop, events) {
  if (!hop) { el.innerHTML = `<div class="empty">没有数据</div>`; return; }
  const reqText = await getText(`/api/traces/${id}/hops/${hop.id}/body?which=req`);
  let req; try { req = JSON.parse(reqText); } catch { el.innerHTML = `<pre>${esc(reqText)}</pre>`; return; }
  let html = conversation(hop.protocol, req);
  const body = events.length ? "" : await getText(`/api/traces/${id}/hops/${hop.id}/body?which=resp`);
  const reply = replyText(hop.protocol, hop, events, body);
  if (reply) html += msgBox("回复", `<div style="white-space:pre-wrap">${esc(reply)}</div>`, "reply");
  el.innerHTML = html || `<div class="empty">无法按 ${esc(hop.protocol || "未知协议")} 解析为对话,请看「原始」。</div>`;
}

// ----- stream tab -----
function tabStream(el, hop, events) {
  if (!hop || !hop.streaming) { el.innerHTML = `<div class="empty">这不是流式响应。</div>`; return; }
  if (!events.length) { el.innerHTML = `<div class="empty">没有收到任何事件。</div>`; return; }
  const limit = Math.max(2000, (new Date(hop.ended) - new Date(hop.started)) * 0.25);
  let prev = 0;
  el.innerHTML = `<table><thead><tr><th>#</th><th>+时间</th><th>间隔</th><th>类型</th><th>内容</th></tr></thead><tbody>${events.map((e) => {
    const t = e.offset_us / 1000, gap = t - prev; prev = t;
    return `<tr><td class="mono">${e.idx}</td><td class="mono">${ms(Math.round(t))}</td><td class="mono ${gap > limit ? "stall" : "muted"}">${ms(Math.round(gap))}</td>
      <td>${e.kind === "comment" ? '<span class="badge b-warn">注释</span>' : esc(e.name || "data")}</td><td class="mono">${esc(e.data.slice(0, 220))}</td></tr>`;
  }).join("")}</tbody></table>`;
}

// ----- error tab -----
async function tabError(el, id, hop) {
  if (!hop) { el.innerHTML = `<div class="empty">没有数据</div>`; return; }
  const body = await getText(`/api/traces/${id}/hops/${hop.id}/body?which=resp`);
  const bad = hop.status >= 400 || hop.error;
  if (!bad) { el.innerHTML = `<div class="empty">这次请求没有错误。</div>`; return; }
  el.innerHTML = `<div class="card"><h3>上游原始响应 ${tierBadge(hop.tier)}</h3>
    <div class="kv"><div>状态码</div><div class="mono">${hop.status || "-"}</div><div>传输层错误</div><div class="mono">${esc(hop.error || "无")}</div>
    <div>响应体大小</div><div class="mono">${body.length} 字节${body.length === 0 ? '<span class="stall"> — 空正文,客户端只能显示一个笼统的错误</span>' : ""}</div></div>
    ${body ? `<h3 style="margin-top:12px">原始响应体</h3><pre>${esc(pretty(body))}</pre>` : ""}
    <h3 style="margin-top:12px">响应头</h3><pre>${esc(JSON.stringify(hop.resp_header || {}, null, 2))}</pre></div>
    <p class="hint">这是中转亲眼看到的上游原始字节。客户端界面上显示的错误可能被改写过。</p>`;
}

// ----- raw tab -----
async function tabRaw(el, id, hops) {
  let html = "";
  for (const h of hops) {
    const req = await getText(`/api/traces/${id}/hops/${h.id}/body?which=req`);
    const resp = await getText(`/api/traces/${id}/hops/${h.id}/body?which=resp`);
    html += `<div class="card"><h3>${esc(h.source)}: ${esc(h.from)} → ${esc(h.to)} ${tierBadge(h.tier)}</h3>
      <div class="kv"><div>${esc(h.method)}</div><div class="mono">${esc(h.uri)}</div><div>状态</div><div class="mono">${h.status}</div></div>
      <details><summary>请求头</summary><pre>${esc(JSON.stringify(h.req_header || {}, null, 2))}</pre></details>
      <h3 style="margin-top:10px">请求体</h3><pre>${esc(pretty(req))}</pre>
      <details><summary>响应头</summary><pre>${esc(JSON.stringify(h.resp_header || {}, null, 2))}</pre></details>
      <h3 style="margin-top:10px">响应体${h.streaming ? "(流式,事件见「流」)" : ""}</h3><pre>${esc(pretty(resp))}</pre></div>`;
  }
  el.innerHTML = html;
}

// ---------- checks ----------
let checkFilter = { outcome: "", q: "" };
async function viewChecks() {
  shell("checks", `<div class="card"><h3>保真度检查</h3><div class="hint">每条用例来自真实 issue。<b>FAIL</b> = 断言被违反,即 issue 描述的行为在这个网关版本上出现;<b>PASS</b> = 网关保留了该保留的内容;
    <b>INCONCLUSIVE</b> = 这次配置无法判定;<b>SKIP</b> = 无法运行。结果来自在 Docker 中对真实网关的运行(customs probe),不是推测。</div></div><div id="ck"></div>`);
  const d = await getJSON("/api/testset");
  const runs = d.results || [], cases = d.cases || [];
  const byRun = runs.map((r) => Object.fromEntries((r.results || []).map((x) => [x.id, x])));
  const el = document.getElementById("ck");
  const draw = () => {
    const rows = cases.filter((c) => (!checkFilter.q || (c.id + c.title).toLowerCase().includes(checkFilter.q.toLowerCase())) &&
      (!checkFilter.outcome || byRun.some((m) => m[c.id] && m[c.id].outcome === checkFilter.outcome)));
    el.innerHTML = `<div class="bar">${["", "FAIL", "PASS", "INCONCLUSIVE", "SKIP"].map((o) => `<button data-o="${o}" class="${checkFilter.outcome === o ? "on" : ""}">${o || "全部"}</button>`).join("")}
      <input id="cq" placeholder="搜索用例" value="${esc(checkFilter.q)}" size="30"><span class="muted">${rows.length} / ${cases.length}</span></div>
      <table><thead><tr><th>用例</th><th>方向</th><th>严重度</th>${runs.map((r) => `<th>${esc(r.gateway)} ${esc(r.version)}</th>`).join("")}</tr></thead><tbody>${rows.map((c) => `
      <tr class="link" data-c="${esc(c.id)}"><td><div>${esc(c.title)}</div><div class="muted mono">${esc(c.id)}</div></td><td>${esc(c.direction)}<div class="muted mono">${esc(c.protocol.in)} → ${esc(c.protocol.out)}</div></td>
      <td>${esc(c.severity)}</td>${byRun.map((m) => { const x = m[c.id]; return `<td>${x ? `<span class="badge b-${esc(x.outcome)}">${esc(x.outcome)}</span>` : '<span class="muted">未跑</span>'}</td>`; }).join("")}</tr>
      <tr id="det-${esc(c.id)}" hidden><td colspan="${3 + runs.length}">${caseDetail(c, runs, byRun)}</td></tr>`).join("")}</tbody></table>`;
    el.querySelectorAll("[data-o]").forEach((b) => (b.onclick = () => { checkFilter.outcome = b.dataset.o; draw(); }));
    el.querySelector("#cq").oninput = debounce((e) => { checkFilter.q = e.target.value; draw(); }, 200);
    el.querySelectorAll("tr.link").forEach((tr) => (tr.onclick = () => { const r = document.getElementById("det-" + tr.dataset.c); r.hidden = !r.hidden; }));
  };
  draw();
}
function caseDetail(c, runs, byRun) {
  return `<div class="kv"><div>来源</div><div><a href="${esc(c.source.url)}" target="_blank" rel="noopener">${esc(c.source.repo)}#${c.source.issue}</a> (${esc(c.source.state)})</div>
    <div>现象</div><div>${esc(c.observed)}</div><div>断言</div><div class="mono">${esc(c.assertions.map(assertionText).join("\n"))}</div>
    <div>payload</div><div>${esc(c.payload_origin === "issue" ? "来自 issue" : "按描述重建")}${c.verified ? ",已在真实网关上运行" : ",未运行"}</div>
    ${c.notes ? `<div>备注</div><div>${esc(c.notes)}</div>` : ""}
    ${runs.map((r, i) => { const x = byRun[i][c.id]; return x ? `<div>${esc(r.gateway)} ${esc(r.version)}</div><div class="mono">${esc(x.outcome)}: ${esc(x.detail)}</div>` : ""; }).join("")}</div>`;
}
function assertionText(a) {
  const items = (a.items || []).map(assertionText).join(" | ");
  return `${a.op} ${a.out || a.in || ""} ${a.text ? JSON.stringify(a.text) : ""} ${a.value !== undefined ? JSON.stringify(a.value) : ""} ${a.texts ? JSON.stringify(a.texts) : ""}${items ? " [" + items + "]" : ""}`.trim();
}

// ---------- sources ----------
async function viewSources() {
  const s = await getJSON("/api/status");
  const info = s.info || {};
  const base = "http://" + (info.relay_listen || "127.0.0.1:9000");
  shell("sources", `<div class="card"><h3>中转</h3><div class="kv"><div>监听</div><div class="mono">${esc(info.relay_listen)}</div><div>数据目录</div><div class="mono">${esc(info.data_dir)}</div>
    <div>版本</div><div class="mono">${esc(info.version)}</div></div></div>
    <div class="card"><h3>目标</h3>${(s.targets || []).length ? `<table><thead><tr><th>名字</th><th>上游</th><th>让客户端用</th></tr></thead><tbody>${s.targets.map((t) => `
      <tr><td>${esc(t.name)}</td><td class="mono">${esc(t.upstream)}</td><td><code class="copy mono" data-c="OPENAI_BASE_URL=${esc(base)}/${esc(t.name)}/v1">OPENAI_BASE_URL=${esc(base)}/${esc(t.name)}/v1</code><br>
      <code class="copy mono" data-c="ANTHROPIC_BASE_URL=${esc(base)}/${esc(t.name)}">ANTHROPIC_BASE_URL=${esc(base)}/${esc(t.name)}</code></td></tr>`).join("")}</tbody></table>` : '<div class="muted">还没有目标。</div>'}
    <div class="bar" style="margin-top:12px"><input id="tn" placeholder="名字,如 litellm" size="14"><input id="tu" placeholder="上游,如 http://127.0.0.1:4000" size="34"><button id="ta">添加目标</button><span id="tmsg" class="muted"></span></div>
    <div class="hint">或在终端:<code class="copy mono" data-c="customs run --target NAME -- 你的命令">customs run --target NAME -- 你的命令</code>,只对这一个子进程设置环境变量,退出即恢复,不改任何配置文件。</div></div>
    <div class="card"><h3>数据来源</h3><table><thead><tr><th>来源</th><th>证据</th><th>状态</th><th>说明</th></tr></thead><tbody>${(info.sources || []).map((x) => `<tr><td>${esc(x.name)}</td><td>${tierBadge(x.tier)}</td>
      <td><span class="badge b-${x.status === "running" ? "ok" : x.status === "error" ? "bad" : "SKIP"}">${esc(x.status)}</span></td><td class="muted">${esc(x.detail)}</td></tr>`).join("")}</tbody></table></div>`);
  document.querySelectorAll("code.copy").forEach((c) => (c.onclick = () => navigator.clipboard && navigator.clipboard.writeText(c.dataset.c)));
  document.getElementById("ta").onclick = async () => {
    const msg = document.getElementById("tmsg");
    try {
      await api("/api/targets", { method: "POST", headers: { "X-Customs-Token": TOKEN, "Content-Type": "application/json" },
        body: JSON.stringify({ name: document.getElementById("tn").value.trim(), upstream: document.getElementById("tu").value.trim() }) });
      viewSources();
    } catch (e) { msg.textContent = e.message; }
  };
}

// ---------- plumbing ----------
function debounce(fn, d) { let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), d); }; }
function route() {
  const h = location.hash.replace(/^#/, "") || "/";
  if (h.startsWith("/trace/")) return viewTrace(h.slice(7));
  if (h === "/checks") return viewChecks();
  if (h === "/sources") return viewSources();
  return viewTraces();
}
window.addEventListener("hashchange", route);
route();
const live = debounce(() => {
  const h = location.hash.replace(/^#/, "") || "/";
  if (h === "/") refreshList();
  else if (h.startsWith("/trace/")) viewTrace(h.slice(7));
}, 300);
try { const es = new EventSource("/api/stream"); es.addEventListener("trace", live); } catch {}
