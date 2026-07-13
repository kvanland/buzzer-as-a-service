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

const storageKey = "buzzer-as-a-service-session";
let session = null;
let state = null;
let events = null;
let audioContext = null;
let renderReady = false;
let lastFirstKey = "";
let lastRound = 0;

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

restoreFromHash();
restoreLocalSession();
setInterval(updateExpiry, 30000);

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
  document.querySelectorAll(".host-only").forEach((node) => node.classList.toggle("hidden", session.role !== "host"));
  renderReady = false;
  connectEvents();
  render(snapshot);
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

  winner.classList.toggle("hot", Boolean(first) && !removed);
  winner.textContent = removed ? "Removed from group" : first ? `${first.playerName} buzzed first!` : snapshot.lockedAll ? "Locked" : "Ready";
  const myBuzz = currentSession ? buzzes.find((buzz) => buzz.playerId === currentSession.playerId) : null;
  buzzer.disabled = Boolean(removed || snapshot.lockedAll || me?.locked || myBuzz);
  statusLine.textContent = removed ? "Ask the host for a new invite." : statusText(snapshot, me, myBuzz);
  lockAllButton.textContent = snapshot.lockedAll ? "Unlock Everyone" : "Lock Everyone";
  setProfileDisabled(removed);
  if (removed) {
    localStorage.removeItem(storageKey);
    document.querySelectorAll(".host-only").forEach((node) => node.classList.add("hidden"));
    if (events) events.close();
    events = null;
    session = null;
    setConnection("Removed", "warn");
  } else {
    updateProfileFields(me);
  }
  updateExpiry();

  renderPlayers(snapshot.players, currentSession && !removed ? currentSession.role : "");
  renderBuzzOrder(buzzes);

  if (!removed && renderReady) {
    if (firstKey && firstKey !== lastFirstKey) feedback("first");
    if (snapshot.round !== lastRound) feedback("reset");
  }
  renderReady = true;
  lastFirstKey = firstKey;
  lastRound = snapshot.round;
}

function renderPlayers(playerList, role) {
  players.innerHTML = "";
  playerList.forEach((player) => {
    const row = document.createElement("div");
    row.className = "player-row";
    const canRemove = role === "host" && !player.isHost;
    row.innerHTML = `<span class="swatch" style="background:${player.color}"></span><div><div class="player-name">${escapeHTML(player.name)}${player.isHost ? " - Host" : ""}</div><div class="player-meta">${player.locked ? "locked" : "ready"} - ${player.lastSeen}</div></div><div class="player-actions host-only ${role === "host" ? "" : "hidden"}"><button data-action="lock" type="button">${player.locked ? "Unlock" : "Lock"}</button><button class="danger ${canRemove ? "" : "hidden"}" data-action="remove" type="button">Remove</button></div>`;
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

async function resetRound() {
  enableFeedback();
  render(await hostAPI("reset", {}));
}

async function resetRoundCount() {
  enableFeedback();
  if (!confirm("Reset the round count back to 1?")) return;
  render(await hostAPI("reset-round-count", {}));
}

async function toggleLockAll() {
  enableFeedback();
  render(await hostAPI("lock-all", { locked: !state.lockedAll }));
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

function leaveRoom() {
  if (events) events.close();
  events = null;
  session = null;
  state = null;
  localStorage.removeItem(storageKey);
  room.classList.add("hidden");
  entry.classList.remove("hidden");
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
