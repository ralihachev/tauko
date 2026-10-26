"use strict";

// ---------- storage & api ----------

const store = {
  get(k, d) { try { return localStorage.getItem(k) ?? d; } catch { return d; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch {} },
};

// A ?token= in the URL is saved once and removed from the address bar.
(() => {
  const url = new URL(location.href);
  const t = url.searchParams.get("token");
  if (t) {
    store.set("token", t);
    url.searchParams.delete("token");
    history.replaceState(null, "", url);
  }
})();

async function api(method, path, body) {
  const headers = { "Content-Type": "application/json" };
  const token = store.get("token", "");
  if (token) headers.Authorization = "Bearer " + token;
  const res = await fetch(path, { method, headers, body: body ? JSON.stringify(body) : undefined });
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
  return res.json();
}

const $ = (sel) => document.querySelector(sel);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);

// ---------- state ----------

const state = {
  view: "learn",
  mode: store.get("mode", "tap"),
  course: "",        // active course, shared with every client through /api/settings
  unit: "",          // learn filter
  card: null,
  next: null,
  phase: "ask",      // ask | revealed
  check: null,
  answer: "",
  last: "",
  quiz: null,        // {unit, cards, i, score, phase, check, answer}
};

// ---------- header ----------

async function refreshCounts() {
  try {
    const s = await api("GET", "/api/stats");
    const streak = s.stats.streak_days ? ` · 🔥${s.stats.streak_days}` : "";
    $("#counts").textContent = `${s.due} due · ${s.new_left} new · ${s.stats.reviews_today} ✓${streak}`;
  } catch (e) {
    $("#counts").textContent = "offline";
  }
}

let lastSignal = "";
async function pollSignal() {
  try {
    const s = await api("GET", "/api/signal");
    const el = $("#signal");
    if (!s.state) { el.hidden = true; return; }
    el.hidden = false;
    el.className = "signal " + s.state;
    el.textContent = s.state === "busy" ? "Claude is working, your turn" : "✓ Claude finished";
    if (s.state === "idle" && lastSignal === "busy" && navigator.vibrate) navigator.vibrate(80);
    if (s.state === "idle" && lastSignal === "busy") document.title = "✓ tauko";
    if (s.state === "busy") document.title = "tauko";
    lastSignal = s.state;
  } catch {}
}

// ---------- course scope ----------

// The scope bar above the card: which course you study, shared with every client.
async function renderScope() {
  const [courses, settings] = await Promise.all([api("GET", "/api/courses"), api("GET", "/api/settings")]);
  state.course = settings.active_course;
  const opts = [`<option value="">All courses</option>`]
    .concat(courses.map((c) => `<option value="${esc(c.id)}">${esc(c.title)} · ${c.seen}/${c.total}</option>`));
  const practice = state.next?.practice
    ? `<span class="practice-note">Practice: nothing due, reviewing what you're closest to forgetting ·
       <button class="link" data-action="more">learn 10 more today</button></span>` : "";
  $("#scope").innerHTML = `<label>Studying <select id="scope-select">${opts.join("")}</select></label>${practice}`;
  $("#scope-select").value = state.course;
  $("#scope-select").onchange = async (e) => { await setCourse(e.target.value); loadNext(); };
}

async function setCourse(id) {
  await api("POST", "/api/settings", { active_course: id });
  state.course = id;
  state.unit = "";
  state.last = "";
  refreshCounts();
}

// ---------- learn ----------

async function loadNext() {
  // Without ?course= the server uses the active course, the one every client shares.
  const q = new URLSearchParams({ exclude: state.last });
  if (state.unit) q.set("unit", state.unit);
  try {
    state.next = await api("GET", "/api/next?" + q);
  } catch (e) {
    $("#card").innerHTML = `<div class="empty"><h2>Server not reachable</h2><p>${esc(e.message)}</p></div>`;
    return;
  }
  state.card = state.next.card;
  renderScope().catch(() => {});
  state.phase = "ask";
  state.check = null;
  state.answer = "";
  state.explanation = "";
  state.explaining = false;
  renderLearn();
}

function cardHead(card, extra = "") {
  return `
    <div class="meta">
      <span class="tag">${esc(card.unit_title)}</span>
      ${extra}
    </div>
    <div class="instruction">${esc(card.instruction)}</div>
    <div class="prompt">${esc(card.prompt)}</div>
    ${card.hint ? `<div class="hint">(${esc(card.hint)})</div>` : ""}`;
}

function revealBlock(card, check, answer) {
  let verdict = "";
  if (check) {
    const label = { correct: "✓ Correct", close: "≈ Almost", wrong: "✗ Wrong" }[check.verdict];
    const why = check.verdict === "close" ? ` <small>(${esc(check.reason)})</small>` : "";
    verdict = `<div class="verdict ${check.verdict}">${label}${why}</div>`;
    if (check.verdict !== "correct" && answer) verdict += `<div class="yours">${esc(answer)}</div>`;
  } else if (answer) {
    verdict = `<div class="translation">Your answer: ${esc(answer)}</div>`;
  }
  return `
    <div class="reveal">
      ${verdict}
      <div class="answers">${card.answers.map(esc).join(" · ")}</div>
      ${card.translation ? `<div class="translation">${esc(card.translation)}</div>` : ""}
      ${card.note ? `<div class="note">💡 ${esc(card.note)}</div>` : ""}
      ${card.example ? `<div class="example">${esc(card.example)}${card.example_translation ? ` <i>(${esc(card.example_translation)})</i>` : ""}</div>` : ""}
    </div>`;
}

function gradeButtons(suggested) {
  const labels = [["Again", "forgot"], ["Hard", "struggled"], ["Good", "recalled"], ["Easy", "instant"]];
  return `<div class="grades">${labels.map(([name, hint], i) =>
    `<button class="g${i + 1}${suggested === i + 1 ? " suggested" : ""}" data-grade="${i + 1}">${name}<small>${hint}</small></button>`).join("")}</div>
    <div class="kbd">1–4 to grade · enter = ${["", "Again", "Hard", "Good", "Easy"][suggested]}</div>`;
}

// chars: extra characters the course needs (ä, ö…) as buttons, for phone keyboards.
function inputBlock(id, chars = [], action = "submit", label = "Check") {
  const keys = chars.length
    ? `<div class="chars">${chars.map((c) => `<button data-char="${esc(c)}">${esc(c)}</button>`).join("")}</div>`
    : "";
  return `
    <input id="${id}" class="input" autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck="false" placeholder="Your answer…">
    ${keys}
    <button class="primary" data-action="${action}">${label}</button>`;
}

function renderLearn() {
  const el = $("#card");
  const { card, next } = state;
  if (!card && next?.no_courses) {
    el.innerHTML = `<div class="empty"><h2>No courses yet</h2>
      <p>Make one about anything, in a terminal:</p>
      <p><code>tauko gen "Kubernetes networking"</code></p>
      <p>or install one from the gallery: <code>tauko add</code></p></div>`;
    return;
  }
  if (!card) {
    el.innerHTML = `<div class="empty"><h2>Nothing to study here yet</h2>
      <p>Learn more cards today, or pick another course above.</p>
      <p><button class="primary" data-action="more">Learn 10 more today</button></p></div>`;
    return;
  }
  const extra = (next.is_new ? `<span class="tag new">new</span>` : "")
    + (next.practice ? `<span class="tag practice">practice</span>` : "")
    + (next.focus ? `<span class="tag">${esc(next.focus)}</span>` : "");
  // A lesson: read it, then straight on to its quick check.
  if (card.kind === "lesson") {
    el.innerHTML = cardHead(card, `<span class="tag lesson">lesson</span>` + extra)
      + `<div class="lesson-body">${esc(card.body)}</div>`
      + (card.example ? `<div class="example">${esc(card.example)}${card.example_translation ? ` <i>(${esc(card.example_translation)})</i>` : ""}</div>` : "")
      + explanationBlock()
      + `<div class="answer-area"><button class="primary" data-action="read">Got it → quick check</button>
         <button class="link" data-action="explain">${state.explaining ? "Asking Claude…" : "Explain differently"}</button>
         <div class="kbd">enter: got it</div></div>`;
    return;
  }
  let body = cardHead(card, extra);
  if (state.phase === "ask") {
    body += `<div class="answer-area">${state.mode === "type"
      ? inputBlock("answer", next.chars || [], "submit", card.self_grade ? "Compare" : "Check")
      : `<button class="primary" data-action="reveal">Show answer</button><div class="kbd">space to reveal</div>`}</div>`;
  } else {
    const suggested = state.check ? state.check.suggested_rating : 3;
    body += revealBlock(card, state.check, state.answer);
    body += explanationBlock();
    body += `<div class="answer-area">${gradeButtons(suggested)}
      <div class="card-actions">
        <button class="link" data-action="explain">${state.explaining ? "Asking Claude…" : "Explain this"}</button>
        <button class="link" data-action="retire">I know this: don't show again</button>
      </div></div>`;
  }
  if (state.lastRetired) {
    body = `<div class="retired-note">✓ Retired “${esc(state.lastRetired.prompt)}” <button class="link" data-action="undo-retire">undo</button></div>` + body;
  }
  el.innerHTML = body;
  if (state.phase === "ask" && state.mode === "type") $("#answer").focus();
}

function explanationBlock() {
  if (state.explaining) return `<div class="note">Asking Claude…</div>`;
  return state.explanation ? `<div class="note why"><b>Why</b><br>${esc(state.explanation)}</div>` : "";
}

// explain asks Claude why the answer is what it is, for what you typed.
async function explain() {
  if (!state.card || state.explaining) return;
  const id = state.card.id;
  state.explaining = true;
  state.explanation = "";
  renderLearn();
  try {
    const res = await api("POST", "/api/explain", { card_id: id, answer: state.answer || "" });
    if (state.card?.id === id) state.explanation = res.text;
  } catch (e) {
    state.explanation = e.message.startsWith("503") ? "Explain needs Claude Code (the claude command) installed." : "Couldn't get an explanation right now.";
  }
  state.explaining = false;
  renderLearn();
}

// retire marks the card "I know this"; the next card offers undo.
async function retire() {
  if (!state.card) return;
  await api("POST", "/api/retire", { card_id: state.card.id });
  state.lastRetired = { id: state.card.id, prompt: state.card.prompt };
  state.last = state.card.id;
  await loadNext();
  refreshCounts();
}

async function unretire(id) {
  await api("POST", "/api/unretire", { card_id: id });
  if (state.lastRetired?.id === id) state.lastRetired = null;
}

async function markRead() {
  if (!state.card || state.card.kind !== "lesson") return;
  await api("POST", "/api/review", { card_id: state.card.id, rating: 3, mode: "lesson" });
  state.last = state.card.id;
  await loadNext();
  refreshCounts();
}

async function submitAnswer() {
  const input = $("#answer");
  if (!input) return;
  state.answer = input.value;
  // Free-form answers can't be auto-checked: show yours next to the reference and self-grade.
  state.check = state.card.self_grade ? null : await api("POST", "/api/check", { card_id: state.card.id, answer: state.answer });
  state.phase = "revealed";
  renderLearn();
}

function reveal() {
  state.phase = "revealed";
  renderLearn();
}

async function grade(rating) {
  const { card, check, answer, mode } = state;
  if (!card || state.phase !== "revealed") return;
  state.lastRetired = null;
  const el = $("#card");
  el.classList.add(rating === 1 ? "out-left" : "out-right");
  await api("POST", "/api/review", {
    card_id: card.id, rating, mode, answer, verdict: check ? check.verdict : "",
  });
  state.last = card.id;
  await loadNext();
  el.classList.remove("out-left", "out-right");
  refreshCounts();
}

function relTime(iso) {
  const min = Math.round((new Date(iso) - Date.now()) / 60000);
  if (min < 1) return "in a moment";
  if (min < 60) return `in ${min} min`;
  const h = Math.round(min / 60);
  return h < 24 ? `in ${h} h` : `in ${Math.round(h / 24)} d`;
}

// ---------- quiz ----------

async function renderQuizStart() {
  const [courses, units] = await Promise.all([api("GET", "/api/courses"), api("GET", "/api/units")]);
  const opts = courses.map((c) => {
    const us = units.filter((u) => u.course === c.id)
      .map((u) => `<option value="unit:${esc(u.id)}">${esc(u.title)}${u.focus ? " · " + esc(u.focus) : ""}</option>`).join("");
    return `<optgroup label="${esc(c.title)}"><option value="course:${esc(c.id)}">All of ${esc(c.title)}</option>${us}</optgroup>`;
  }).join("");
  $("#quiz").innerHTML = `
    <div class="instruction">Self-quiz</div>
    <div class="prompt">10 questions, typed, scored.</div>
    <div class="hint">Mostly drills. Answers also feed your review schedule.</div>
    <div class="answer-area">
      <select id="quiz-unit" class="input"><option value="">Everything</option>${opts}</select>
      <button class="primary" data-action="quiz-start">Start</button>
    </div>`;
  if (state.quizUnit) $("#quiz-unit").value = "unit:" + state.quizUnit;
}

// scope is "", "course:<id>" or "unit:<id>".
async function startQuiz(scope) {
  const [kind, id] = scope.split(":");
  const sel = { course: kind === "course" ? id : "", unit: kind === "unit" ? id : "" };
  const { cards } = await api("POST", "/api/quiz", { ...sel, n: 10 });
  state.quiz = { ...sel, cards, i: 0, score: 0, phase: "ask", check: null, answer: "" };
  renderQuiz();
}

function renderQuiz() {
  const q = state.quiz;
  const el = $("#quiz");
  if (q.i >= q.cards.length) {
    const pct = Math.round(100 * q.score / q.cards.length);
    const word = pct >= 90 ? "Excellent!" : pct >= 75 ? "Good!" : pct >= 50 ? "Getting there." : "More practice needed.";
    el.innerHTML = `<div class="empty"><div class="instruction">Score</div>
      <div class="score">${q.score}/${q.cards.length}</div><h2>${word}</h2>
      <p><button class="primary" data-action="quiz-again">New quiz</button></p></div>`;
    return;
  }
  const card = q.cards[q.i];
  let body = cardHead(card, `<span class="tag">${q.i + 1} / ${q.cards.length}</span>`);
  if (q.phase === "ask") {
    body += `<div class="answer-area">${inputBlock("quiz-answer")}</div>`;
  } else {
    body += revealBlock(card, q.check, q.answer);
    body += `<div class="answer-area"><button class="primary" data-action="quiz-next">Next →</button><div class="kbd">enter for next</div></div>`;
  }
  el.innerHTML = body;
  if (q.phase === "ask") $("#quiz-answer").focus();
}

async function submitQuiz() {
  const q = state.quiz;
  const card = q.cards[q.i];
  q.answer = $("#quiz-answer").value;
  q.check = await api("POST", "/api/check", { card_id: card.id, answer: q.answer });
  if (q.check.verdict !== "wrong") q.score++;
  q.phase = "revealed";
  renderQuiz();
  api("POST", "/api/review", { card_id: card.id, rating: q.check.suggested_rating, mode: "quiz", answer: q.answer, verdict: q.check.verdict });
}

async function nextQuiz() {
  const q = state.quiz;
  q.i++;
  q.phase = "ask";
  q.check = null;
  if (q.i >= q.cards.length) {
    await api("POST", "/api/quiz/result", { course: q.course, unit: q.unit, score: q.score, total: q.cards.length });
    refreshCounts();
  }
  renderQuiz();
}

// ---------- units ----------

async function renderUnits() {
  const [courses, units, retired] = await Promise.all([api("GET", "/api/courses"), api("GET", "/api/units"), api("GET", "/api/retired")]);
  const retiredList = (course) => {
    const rs = retired.filter((r) => r.course === course);
    if (!rs.length) return "";
    return `<details class="unit"><summary>Retired cards (${rs.length}): you marked these "I know this"</summary>
      ${rs.map((r) => `<div class="retired-row"><span>${esc(r.prompt)} → <b>${esc(r.answer)}</b></span>
        <button class="link" data-unretire="${esc(r.id)}">bring back</button></div>`).join("")}</details>`;
  };
  const unitCard = (u, focusLabel) => {
    const pct = u.total ? Math.round(100 * u.seen / u.total) : 0;
    const quiz = u.best_quiz != null ? ` · quiz ${Math.round(u.best_quiz * 100)}%` : "";
    const sub = [u.subtitle, u.focus].filter(Boolean).map(esc).join(" · ");
    return `<div class="unit">
      <div class="unit-head"><b>${esc(u.title)}</b><span>${u.seen}/${u.total}${u.due ? ` · ${u.due} due` : ""}${quiz}</span></div>
      ${sub ? `<div class="hint">${sub}</div>` : ""}
      <div class="bar"><div style="width:${pct}%"></div></div>
      ${u.focus_note ? `<details><summary>${esc(focusLabel || "Notes")}</summary>${esc(u.focus_note)}</details>` : ""}
      ${(u.lessons || []).map((l) => `<details><summary>${l.read ? "✓" : "○"} Lesson: ${esc(l.title)}</summary>${esc(l.body)}${l.example ? `<br><i>${esc(l.example)}</i>` : ""}</details>`).join("")}
      <div class="unit-actions">
        <button data-learn-unit="${esc(u.id)}">Practice</button>
        <button data-quiz-unit="${esc(u.id)}">Quiz</button>
      </div>
    </div>`;
  };
  $("#units").innerHTML = courses.map((c) => `
    <div class="course-head">
      <h2>${esc(c.title)}${c.title_en && c.title_en !== c.title ? ` <small>${esc(c.title_en)}</small>` : ""}</h2>
      <span>${c.seen}/${c.total} · ${c.due} due</span>
    </div>
    ${c.description ? `<div class="hint">${esc(c.description)}</div>` : ""}
    <button class="primary" data-learn-course="${esc(c.id)}">Practice this course</button>
    ${units.filter((u) => u.course === c.id).map((u) => unitCard(u, c.focus_label)).join("")}
    ${retiredList(c.id)}
  `).join("") + `<button class="primary" data-learn-course="">Practice everything</button>`;
}

// ---------- navigation & input ----------

function show(view) {
  state.view = view;
  document.querySelectorAll("nav > button").forEach((b) => b.classList.toggle("active", b.dataset.view === view));
  for (const v of ["learn", "quiz", "units"]) $(`#view-${v}`).hidden = v !== view;
  if (view === "learn") loadNext();
  if (view === "quiz") { state.quiz = null; renderQuizStart(); }
  if (view === "units") renderUnits();
}

function setMode(mode) {
  state.mode = mode;
  store.set("mode", mode);
  document.querySelectorAll("[data-mode]").forEach((b) => b.classList.toggle("active", b.dataset.mode === mode));
  if (state.view === "learn" && state.phase === "ask") renderLearn();
}

document.addEventListener("click", async (e) => {
  const t = e.target.closest("button");
  if (!t) return;
  const d = t.dataset;
  try {
    if (d.view) show(d.view);
    else if (d.mode) setMode(d.mode);
    else if (d.goto) show(d.goto);
    else if (d.grade) await grade(Number(d.grade));
    else if (d.char) insertChar(d.char);
    else if (d.learnUnit !== undefined) { state.unit = d.learnUnit; state.last = ""; show("learn"); }
    else if (d.learnCourse !== undefined) { await setCourse(d.learnCourse); show("learn"); }
    else if (d.action === "more") { await api("POST", "/api/more", { n: 10 }); await loadNext(); refreshCounts(); }
    else if (d.quizUnit) { state.quizUnit = d.quizUnit; show("quiz"); }
    else if (d.action === "reveal") reveal();
    else if (d.action === "read") await markRead();
    else if (d.action === "retire") await retire();
    else if (d.action === "undo-retire") { await unretire(state.lastRetired.id); state.last = ""; await loadNext(); refreshCounts(); }
    else if (d.unretire) { await unretire(d.unretire); renderUnits(); }
    else if (d.action === "explain") await explain();
    else if (d.action === "submit") await submitAnswer();
    else if (d.action === "quiz-start") await startQuiz($("#quiz-unit").value);
    else if (d.action === "quiz-next") await nextQuiz();
    else if (d.action === "quiz-again") renderQuizStart();
  } catch (err) {
    alert(err.message);
  }
});

function insertChar(ch) {
  const input = $("#answer") || $("#quiz-answer");
  if (!input) return;
  const { selectionStart: s, selectionEnd: en, value } = input;
  input.value = value.slice(0, s) + ch + value.slice(en);
  input.focus();
  input.setSelectionRange(s + 1, s + 1);
}

document.addEventListener("keydown", async (e) => {
  if (e.metaKey || e.ctrlKey || e.altKey) return;
  const typing = e.target.tagName === "INPUT";
  try {
    if (state.view === "learn") {
      if (state.card?.kind === "lesson") {
        if (e.key === "Enter") { e.preventDefault(); await markRead(); }
        else if (e.key === "e") await explain();
      } else if (state.phase === "ask") {
        if (typing && e.key === "Enter") { e.preventDefault(); await submitAnswer(); }
        else if (!typing && state.mode === "tap" && (e.key === " " || e.key === "Enter")) { e.preventDefault(); reveal(); }
      } else if (e.key === "e") {
        await explain();
      } else if (e.key === "k") {
        await retire();
      } else if (["1", "2", "3", "4"].includes(e.key)) {
        await grade(Number(e.key));
      } else if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        await grade(state.check ? state.check.suggested_rating : 3);
      }
    } else if (state.view === "quiz" && state.quiz) {
      if (state.quiz.phase === "ask" && typing && e.key === "Enter") { e.preventDefault(); await submitQuiz(); }
      else if (state.quiz.phase === "revealed" && e.key === "Enter") { e.preventDefault(); await nextQuiz(); }
    }
  } catch (err) {
    alert(err.message);
  }
});

// Swipe on a revealed card: left = Again, right = suggested grade.
let touchX = null;
$("#card").addEventListener("touchstart", (e) => { touchX = e.touches[0].clientX; }, { passive: true });
$("#card").addEventListener("touchend", (e) => {
  if (touchX === null || state.phase !== "revealed") return;
  const dx = e.changedTouches[0].clientX - touchX;
  touchX = null;
  if (Math.abs(dx) < 70) return;
  grade(dx < 0 ? 1 : (state.check ? state.check.suggested_rating : 3));
});

// ---------- boot ----------

setMode(state.mode);
show("learn");
refreshCounts();
pollSignal();
setInterval(pollSignal, 3000);
setInterval(refreshCounts, 60000);
if ("serviceWorker" in navigator) navigator.serviceWorker.register("sw.js").catch(() => {});
