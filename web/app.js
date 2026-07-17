const entry = document.querySelector("#entry");
const room = document.querySelector("#room");
const startTab = document.querySelector("#startTab");
const joinTab = document.querySelector("#joinTab");
const startForm = document.querySelector("#startForm");
const joinForm = document.querySelector("#joinForm");
const profileForm = document.querySelector("#profileForm");
const copyCode = document.querySelector("#copyCode");
const copyInvite = document.querySelector("#copyInvite");
const copyRecovery = document.querySelector("#copyRecovery");
const leaveButton = document.querySelector("#leave");
const buzzer = document.querySelector("#buzzer");
const winner = document.querySelector("#winner");
const statusLine = document.querySelector("#status");
const resetButton = document.querySelector("#reset");
const resetRoundsButton = document.querySelector("#resetRounds");
const lockAllButton = document.querySelector("#lockAll");
const players = document.querySelector("#players");
const buzzOrder = document.querySelector("#buzzOrder");
const toast = document.querySelector("#toast");
const roomName = document.querySelector("#roomName");
const roomColor = document.querySelector("#roomColor");
const connectionStatus = document.querySelector("#connectionStatus");
const expiresAt = document.querySelector("#expiresAt");
const buzzerViewTab = document.querySelector("#buzzerViewTab");
const roomViewTab = document.querySelector("#roomViewTab");
const answerPanel = document.querySelector("#answerPanel");
const answerForm = document.querySelector("#answerForm");
const answerText = document.querySelector("#answerText");
const submitAnswerButton = document.querySelector("#submitAnswer");
const hostAnswerPanel = document.querySelector("#hostAnswerPanel");
const answerProgress = document.querySelector("#answerProgress");
const answerResults = document.querySelector("#answerResults");
const gameMode = document.querySelector("#gameMode");

const storageKey = "buzzer-as-a-service-session";
let session = null;
let state = null;
let events = null;
let audioContext = null;
let renderReady = false;
let lastFirstKey = "";
let lastRound = 0;
let lastMode = "";
let answerFetchKey = "";
let answerFetchSequence = 0;
let heartbeatTimer = null;

const heartbeatEveryMs = 60000;
const possiblyDisconnectedAfterMs = 150000;

startTab.addEventListener("click", () => selectTab("start"));
joinTab.addEventListener("click", () => selectTab("join"));
startForm.addEventListener("submit", startGroup);
joinForm.addEventListener("submit", joinGroup);
profileForm.addEventListener("submit", saveProfile);
copyCode.addEventListener("click", () => copyText(state?.code || session?.code || ""));
copyInvite.addEventListener("click", copyInviteLink);
copyRecovery.addEventListener("click", copyHostLink);
leaveButton.addEventListener("click", leaveRoom);
buzzer.addEventListener("click", buzz);
resetButton.addEventListener("click", resetRound);
resetRoundsButton.addEventListener("click", resetRoundCount);
lockAllButton.addEventListener("click", toggleLockAll);
buzzerViewTab.addEventListener("click", () => setMobileView("buzzer"));
roomViewTab.addEventListener("click", () => setMobileView("room"));
answerForm.addEventListener("submit", submitAnswer);
gameMode.addEventListener("change", changeGameMode);

restoreFromHash();
restoreLocalSession();
setInterval(() => {
  updateExpiry();
  refreshPresence();
}, 30000);
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) sendHeartbeat();
});

function selectTab(tab) {
  const start = tab === "start";
  startTab.classList.toggle("active", start);
  joinTab.classList.toggle("active", !start);
  startForm.classList.toggle("hidden", !start);
  joinForm.classList.toggle("hidden", start);
}

async function startGroup(event) {
  event.preventDefault();
  enableFeedback();
  const result = await api("api/groups", { hostName: value("#hostName") || "Host", color: value("#hostColor") });
  session = { role: "host", code: result.code, hostToken: result.hostToken, playerId: result.playerId, playerToken: result.playerToken };
  saveSession();
  enterRoom(result.snapshot);
}

async function joinGroup(event) {
  event.preventDefault();
  enableFeedback();
  const code = value("#joinCode").toUpperCase();
  const existing = loadSession();
  const body = { name: value("#playerName") || "Contestant", color: value("#playerColor") };
  if (existing?.code === code && existing?.role === "player") {
    body.playerId = existing.playerId;
    body.playerToken = existing.playerToken;
  }
  const result = await api(`api/groups/${code}/join`, body);
  session = { role: "player", code, playerId: result.playerId, playerToken: result.playerToken };
  saveSession();
  enterRoom(result.snapshot);
}

async function restoreLocalSession() {
  if (session) return;
  const saved = loadSession();
  if (!saved) return;
  try {
    if (saved.role === "host") {
      const result = await api(`api/groups/${saved.code}/host-session`, { hostToken: saved.hostToken });
      session = { ...saved, playerId: result.playerId, playerToken: result.playerToken };
      saveSession();
      enterRoom(result.snapshot);
    } else {
      const result = await api(`api/groups/${saved.code}/player-session`, { playerId: saved.playerId, playerToken: saved.playerToken });
      session = saved;
      enterRoom(result.snapshot);
    }
  } catch {
    localStorage.removeItem(storageKey);
  }
}

async function restoreFromHash() {
  const hash = new URLSearchParams(location.hash.slice(1));
  if (hash.has("join")) {
    selectTab("join");
    document.querySelector("#joinCode").value = hash.get("join");
  }
  if (!hash.has("host")) return;
  try {
    const recovered = JSON.parse(atob(hash.get("host")));
    const result = await api(`api/groups/${recovered.code}/host-session`, { hostToken: recovered.hostToken });
    session = { role: "host", code: recovered.code, hostToken: recovered.hostToken, playerId: result.playerId, playerToken: result.playerToken };
    history.replaceState(null, "", location.pathname);
    saveSession();
    enterRoom(result.snapshot);
  } catch {
    showToast("Host link could not be restored.");
  }
}

function enterRoom(snapshot) {
  entry.classList.add("hidden");
  room.classList.remove("hidden");
  setMobileView("buzzer", false);
  document.querySelectorAll(".host-only").forEach((node) => node.classList.toggle("hidden", session.role !== "host"));
  renderReady = false;
  answerFetchKey = "";
  answerResults.innerHTML = "";
  connectEvents();
  startHeartbeat();
  render(snapshot);
}

function setMobileView(view, focus = true) {
  const showBuzzer = view === "buzzer";
  room.dataset.mobileView = view;
  buzzerViewTab.classList.toggle("active", showBuzzer);
  roomViewTab.classList.toggle("active", !showBuzzer);
  buzzerViewTab.setAttribute("aria-selected", String(showBuzzer));
  roomViewTab.setAttribute("aria-selected", String(!showBuzzer));
  if (focus && window.matchMedia("(max-width: 860px)").matches) {
    room.scrollIntoView({ behavior: "smooth", block: "start" });
  }
}

function connectEvents() {
  if (events) events.close();
  setConnection("Connecting", "");
  events = new EventSource(`api/groups/${session.code}/events`);
  events.addEventListener("open", () => setConnection("Live", "live"));
  events.addEventListener("state", (event) => render(JSON.parse(event.data)));
  events.addEventListener("error", () => setConnection("Reconnecting", "warn"));
}

function render(snapshot) {
  if (!snapshot?.code) return;
  state = snapshot;
  copyCode.textContent = snapshot.code;
  const currentSession = session;
  const me = currentSession ? snapshot.players.find((player) => player.id === currentSession.playerId) : null;
  const first = snapshot.firstBuzz;
  const buzzes = snapshot.buzzes || [];
  const firstKey = first ? `${snapshot.round}:${first.playerId}:${first.at}` : "";
  const removed = Boolean(currentSession && !me);
  const answerMode = snapshot.mode === "answers";
  const isHost = currentSession?.role === "host";

  if (renderReady && (snapshot.round !== lastRound || snapshot.mode !== lastMode)) answerText.value = "";

  document.querySelectorAll(".buzzer-mode").forEach((node) => node.classList.toggle("hidden", answerMode));
  answerPanel.classList.toggle("hidden", !answerMode);
  gameMode.value = snapshot.mode || "buzzer";
  buzzerViewTab.textContent = answerMode ? "Answer" : "Buzzer";
  resetButton.textContent = answerMode ? "New Answer Round" : "Reset";
  lockAllButton.classList.toggle("hidden", answerMode);

  winner.classList.toggle("hot", !answerMode && Boolean(first) && !removed);
  winner.textContent = removed ? "Removed from group" : answerMode ? answerWinnerText(snapshot, isHost) : first ? `${first.playerName} buzzed first!` : snapshot.lockedAll ? "Locked" : "Ready";
  const myBuzz = currentSession ? buzzes.find((buzz) => buzz.playerId === currentSession.playerId) : null;
  buzzer.disabled = Boolean(answerMode || removed || snapshot.lockedAll || me?.locked || myBuzz);
  statusLine.textContent = removed ? "Ask the host for a new invite." : answerMode ? answerStatusText(snapshot, me, isHost) : statusText(snapshot, me, myBuzz);
  const anyLocks = snapshot.lockedAll || snapshot.players.some((player) => player.locked);
  lockAllButton.textContent = anyLocks ? "Unlock Everyone" : "Lock Everyone";
  setProfileDisabled(removed);
  if (removed) {
    localStorage.removeItem(storageKey);
    document.querySelectorAll(".host-only").forEach((node) => node.classList.add("hidden"));
    if (events) events.close();
    events = null;
    stopHeartbeat();
    session = null;
    setConnection("Removed", "warn");
  } else {
    updateProfileFields(me);
  }
  updateExpiry();

  renderPlayers(snapshot.players, currentSession && !removed ? currentSession.role : "");
  renderBuzzOrder(buzzes);
  renderAnswerMode(snapshot, me, isHost, removed);

  if (!removed && renderReady) {
    if (firstKey && firstKey !== lastFirstKey) feedback("first");
    if (snapshot.round !== lastRound) feedback("reset");
  }
  renderReady = true;
  lastFirstKey = firstKey;
  lastRound = snapshot.round;
  lastMode = snapshot.mode;
}

function answerWinnerText(snapshot, isHost) {
  if (snapshot.answersRevealed) return isHost ? "Answers revealed!" : "All answers are in!";
  return `${snapshot.submittedCount} of ${snapshot.expectedAnswerCount} submitted`;
}

function answerStatusText(snapshot, me, isHost) {
  if (isHost) return snapshot.answersRevealed ? "Review the answers below." : "Answers stay hidden until everyone submits.";
  if (snapshot.answersRevealed) return "The host can now see the answers.";
  if (me?.submitted) return "Answer submitted. You can update it until reveal.";
  return "Write an answer and submit when ready.";
}

function renderAnswerMode(snapshot, me, isHost, removed) {
  if (snapshot.mode !== "answers") {
    answerFetchKey = "";
    answerResults.innerHTML = "";
    return;
  }
  answerForm.classList.toggle("hidden", isHost || removed || snapshot.answersRevealed);
  hostAnswerPanel.classList.toggle("hidden", !isHost);
  answerText.disabled = Boolean(removed || snapshot.answersRevealed);
  submitAnswerButton.textContent = me?.submitted ? "Update Answer" : "Submit Answer";
  answerProgress.textContent = snapshot.answersRevealed ? "Everyone has submitted." : `Waiting for ${snapshot.expectedAnswerCount - snapshot.submittedCount} more answer${snapshot.expectedAnswerCount - snapshot.submittedCount === 1 ? "" : "s"}.`;
  if (!isHost || !snapshot.answersRevealed) {
    answerResults.innerHTML = "";
    answerFetchKey = "";
    return;
  }
  loadHostAnswers(snapshot);
}

async function loadHostAnswers(snapshot) {
  const submittedPlayers = snapshot.players.filter((player) => player.submitted).map((player) => `${player.id}:${player.name}:${player.color}`).join("|");
  const key = `${snapshot.round}:${snapshot.submittedCount}:${snapshot.expectedAnswerCount}:${submittedPlayers}`;
  if (answerFetchKey === key) return;
  answerFetchKey = key;
  const sequence = ++answerFetchSequence;
  try {
    const result = await hostAPI("answers", {});
    if (sequence !== answerFetchSequence || state?.mode !== "answers" || !state?.answersRevealed) return;
    renderAnswerResults(result.answers || []);
  } catch {
    if (answerFetchKey === key) answerFetchKey = "";
  }
}

function renderAnswerResults(answers) {
  answerResults.innerHTML = "";
  answers.forEach((answer) => {
    const card = document.createElement("article");
    card.className = "answer-card";
    card.innerHTML = `<div class="answer-card-head"><span class="swatch" style="background:${answer.color}"></span><span class="player-name">${escapeHTML(answer.playerName)}</span></div><p class="answer-text">${escapeHTML(answer.text)}</p>`;
    answerResults.append(card);
  });
}

function renderPlayers(playerList, role) {
  players.innerHTML = "";
  playerList.forEach((player) => {
    const row = document.createElement("div");
    row.className = "player-row";
    const canRemove = role === "host" && !player.isHost;
    const playerState = state?.mode === "answers" && !player.isHost ? (player.submitted ? "submitted" : state?.answersRevealed ? "joined after reveal" : "waiting") : (player.locked ? "locked" : "ready");
    row.innerHTML = `<span class="swatch" style="background:${player.color}"></span><div><div class="player-name">${escapeHTML(player.name)}${player.isHost ? " - Host" : ""}</div><div class="player-meta ${player.submitted ? "submitted-badge" : ""}">${playerState} - ${presenceText(player, role)}</div></div><div class="player-actions host-only ${role === "host" ? "" : "hidden"}"><button class="${state?.mode === "answers" ? "hidden" : ""}" data-action="lock" type="button">${player.locked ? "Unlock" : "Lock"}</button><button class="danger ${canRemove ? "" : "hidden"}" data-action="remove" type="button">Remove</button></div>`;
    row.querySelector('[data-action="lock"]').addEventListener("click", () => setPlayerLock(player.id, !player.locked));
    const remove = row.querySelector('[data-action="remove"]');
    if (remove) remove.addEventListener("click", () => removePlayer(player));
    players.append(row);
  });
}

function renderBuzzOrder(buzzes) {
  buzzOrder.innerHTML = "";
  if (buzzes.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty-order";
    empty.textContent = "No buzzes yet";
    buzzOrder.append(empty);
    return;
  }
  buzzes.forEach((buzz) => {
    const row = document.createElement("div");
    row.className = "buzz-row";
    row.innerHTML = `<span class="buzz-rank">${buzz.order}</span><span class="swatch" style="background:${buzz.color}"></span><div class="player-name">${escapeHTML(buzz.playerName)}</div>`;
    buzzOrder.append(row);
  });
}

function setProfileDisabled(disabled) {
  profileForm.querySelectorAll("input, button").forEach((control) => {
    control.disabled = disabled;
  });
}

function updateProfileFields(me) {
  if (!me) return;
  if (document.activeElement !== roomName) roomName.value = me.name;
  if (document.activeElement !== roomColor) roomColor.value = me.color;
}

function statusText(snapshot, me, myBuzz) {
  if (!me) return "Session needs a refresh.";
  if (myBuzz?.order === 1) return "You got it first.";
  if (myBuzz) return `You are #${myBuzz.order} on the board.`;
  if (snapshot.lockedAll) return "The host locked the room.";
  if (me.locked) return "You are locked out this round.";
  if (snapshot.firstBuzz) return `${snapshot.firstBuzz.playerName} is first. Buzz for the board.`;
  return `Round ${snapshot.round}. Palm ready.`;
}

async function saveProfile(event) {
  event.preventDefault();
  enableFeedback();
  const result = await api(`api/groups/${session.code}/profile`, {
    playerId: session.playerId,
    playerToken: session.playerToken,
    name: roomName.value,
    color: roomColor.value
  });
  render(result.snapshot);
  showToast("Updated.");
}

async function buzz() {
  enableFeedback();
  const result = await api(`api/groups/${session.code}/buzz`, { playerId: session.playerId, playerToken: session.playerToken });
  render(result.snapshot);
}

async function submitAnswer(event) {
  event.preventDefault();
  const text = answerText.value.trim();
  if (!text) {
    showToast("Enter an answer first.");
    return;
  }
  const result = await api(`api/groups/${session.code}/answer`, { playerId: session.playerId, playerToken: session.playerToken, text });
  render(result.snapshot);
  showToast(result.snapshot.answersRevealed ? "Submitted. Answers revealed." : "Answer submitted.");
}

async function changeGameMode() {
  const mode = gameMode.value;
  const hasRoundActivity = Boolean(state?.buzzes?.length || state?.submittedCount);
  if (hasRoundActivity && !confirm("Changing modes clears the current round. Continue?")) {
    gameMode.value = state?.mode || "buzzer";
    return;
  }
  try {
    render(await hostAPI("mode", { mode }));
    answerText.value = "";
  } catch {
    gameMode.value = state?.mode || "buzzer";
  }
}

async function resetRound() {
  enableFeedback();
  render(await hostAPI("reset", {}));
  answerText.value = "";
}

async function resetRoundCount() {
  enableFeedback();
  if (!confirm("Reset the round count back to 1?")) return;
  render(await hostAPI("reset-round-count", {}));
}

async function toggleLockAll() {
  enableFeedback();
  const anyLocks = state?.lockedAll || state?.players?.some((player) => player.locked);
  render(await hostAPI("lock-all", { locked: !anyLocks }));
}

async function setPlayerLock(playerId, locked) {
  enableFeedback();
  render(await hostAPI(`players/${playerId}/lock`, { locked }));
}

async function removePlayer(player) {
  enableFeedback();
  if (!confirm(`Remove ${player.name} from this group?`)) return;
  render(await hostAPI(`players/${player.id}/remove`, {}));
}

function hostAPI(action, body) { return api(`api/groups/${session.code}/${action}`, { ...body, hostToken: session.hostToken }); }
function copyInviteLink() { copyText(`${location.origin}${basePath()}#join=${state.code}`); }
function copyHostLink() { copyText(`${location.origin}${basePath()}#host=${btoa(JSON.stringify({ code: session.code, hostToken: session.hostToken }))}`); }

async function leaveRoom() {
  const leavingSession = session;
  if (leavingSession?.role === "player") {
    leaveButton.disabled = true;
    try {
      await api(`api/groups/${leavingSession.code}/leave`, { playerId: leavingSession.playerId, playerToken: leavingSession.playerToken });
    } catch {
      leaveButton.disabled = false;
      return;
    }
  }
  exitRoom();
  leaveButton.disabled = false;
}

function exitRoom() {
  if (events) events.close();
  events = null;
  stopHeartbeat();
  session = null;
  state = null;
  localStorage.removeItem(storageKey);
  room.classList.add("hidden");
  entry.classList.remove("hidden");
}

function startHeartbeat() {
  stopHeartbeat();
  heartbeatTimer = setInterval(sendHeartbeat, heartbeatEveryMs);
}

function stopHeartbeat() {
  if (heartbeatTimer) clearInterval(heartbeatTimer);
  heartbeatTimer = null;
}

async function sendHeartbeat() {
  const activeSession = session;
  if (!activeSession) return;
  try {
    await fetch(`api/groups/${activeSession.code}/heartbeat`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ playerId: activeSession.playerId, playerToken: activeSession.playerToken })
    });
  } catch {
    // The live event stream owns connection messaging and reconnection.
  }
}

function refreshPresence() {
  if (!state?.players || !session) return;
  renderPlayers(state.players, session.role);
}

function presenceText(player, role) {
  const seenAt = new Date(player.lastSeenAt).getTime();
  if (!Number.isFinite(seenAt)) return player.lastSeen;
  const age = Math.max(0, Date.now() - seenAt);
  const relative = age < 60000 ? "just now" : age < 3600000 ? `${Math.floor(age / 60000)}m ago` : `${Math.floor(age / 3600000)}h ago`;
  if (role === "host" && !player.isHost && age >= possiblyDisconnectedAfterMs) return `possibly disconnected - ${relative}`;
  return relative;
}

async function api(path, body) {
  const response = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    showToast(data.error || "Request failed.");
    throw new Error(data.error || response.statusText);
  }
  return data;
}

async function copyText(text) {
  if (!text) return;
  await navigator.clipboard.writeText(text);
  showToast("Copied.");
}

function updateExpiry() {
  if (!state?.expiresAt) return;
  const ms = new Date(state.expiresAt).getTime() - Date.now();
  const minutes = Math.max(0, Math.ceil(ms / 60000));
  const hours = Math.floor(minutes / 60);
  const mins = minutes % 60;
  expiresAt.textContent = hours > 0 ? `Expires in ${hours}h ${mins}m` : `Expires in ${mins}m`;
}

function setConnection(text, mode) {
  connectionStatus.textContent = text;
  connectionStatus.classList.toggle("live", mode === "live");
  connectionStatus.classList.toggle("warn", mode === "warn");
}

function enableFeedback() {
  if (!window.AudioContext && !window.webkitAudioContext) return;
  if (!audioContext) audioContext = new (window.AudioContext || window.webkitAudioContext)();
  if (audioContext.state === "suspended") audioContext.resume();
}

function feedback(kind) {
  if (navigator.vibrate) navigator.vibrate(kind === "first" ? [25, 30, 50] : 25);
  if (!audioContext || audioContext.state !== "running") return;
  const osc = audioContext.createOscillator();
  const gain = audioContext.createGain();
  osc.type = "square";
  osc.frequency.value = kind === "first" ? 740 : 420;
  gain.gain.setValueAtTime(0.0001, audioContext.currentTime);
  gain.gain.exponentialRampToValueAtTime(0.08, audioContext.currentTime + 0.01);
  gain.gain.exponentialRampToValueAtTime(0.0001, audioContext.currentTime + 0.12);
  osc.connect(gain).connect(audioContext.destination);
  osc.start();
  osc.stop(audioContext.currentTime + 0.14);
}

function value(selector) { return document.querySelector(selector).value.trim(); }
function saveSession() { localStorage.setItem(storageKey, JSON.stringify(session)); }
function loadSession() { try { return JSON.parse(localStorage.getItem(storageKey)); } catch { return null; } }
function basePath() { return location.pathname.includes("/buzzer-as-a-service/") ? "/buzzer-as-a-service/" : (location.pathname.endsWith("/") ? location.pathname : `${location.pathname}/`); }
function showToast(message) { toast.textContent = message; toast.classList.add("show"); clearTimeout(showToast.timer); showToast.timer = setTimeout(() => toast.classList.remove("show"), 1800); }
function escapeHTML(text) { const div = document.createElement("div"); div.textContent = text; return div.innerHTML; }
