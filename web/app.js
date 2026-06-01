const els = {
  serverHint: document.querySelector("#serverHint"),
  sessionList: document.querySelector("#sessionList"),
  newSessionBtn: document.querySelector("#newSessionBtn"),
  advancedToggle: document.querySelector("#advancedToggle"),
  advancedPanel: document.querySelector("#advancedPanel"),
  hostInput: document.querySelector("#hostInput"),
  portInput: document.querySelector("#portInput"),
  usernameInput: document.querySelector("#usernameInput"),
  authSelect: document.querySelector("#authSelect"),
  passwordField: document.querySelector("#passwordField"),
  passwordInput: document.querySelector("#passwordInput"),
  keyField: document.querySelector("#keyField"),
  privateKeyInput: document.querySelector("#privateKeyInput"),
  passphraseField: document.querySelector("#passphraseField"),
  passphraseInput: document.querySelector("#passphraseInput"),
  tokenField: document.querySelector("#tokenField"),
  tokenInput: document.querySelector("#tokenInput"),
  connectBtn: document.querySelector("#connectBtn"),
  disconnectBtn: document.querySelector("#disconnectBtn"),
  terminalTitle: document.querySelector("#terminalTitle"),
  terminalStack: document.querySelector("#terminalStack"),
  clearBtn: document.querySelector("#clearBtn"),
  toast: document.querySelector("#toast"),
};

const config = {
  localHost: "127.0.0.1",
  localPort: 22,
  requiresToken: false,
};

const state = {
  sessions: [],
  activeId: null,
  counter: 0,
};

const terminalRowGuard = 1;

const terminalTheme = {
  background: "#101312",
  foreground: "#f1f3e8",
  cursor: "#7bd66f",
  selectionBackground: "#365f31",
  black: "#0d0f0e",
  red: "#f26d6d",
  green: "#7bd66f",
  yellow: "#e7b95b",
  blue: "#5c91ff",
  magenta: "#d386e8",
  cyan: "#73d7d8",
  white: "#f1f3e8",
  brightBlack: "#666f65",
  brightRed: "#ff8f8f",
  brightGreen: "#9ff095",
  brightYellow: "#f0ca73",
  brightBlue: "#86aeff",
  brightMagenta: "#e2a2f0",
  brightCyan: "#98eeee",
  brightWhite: "#ffffff",
};

bootstrap();

async function bootstrap() {
  wireUI();
  await loadConfig();
  createSession();
  if (window.lucide) {
    window.lucide.createIcons();
  }
}

async function loadConfig() {
  try {
    const response = await fetch("/api/config");
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    Object.assign(config, await response.json());
    els.serverHint.textContent = `默认 ${config.localHost}:${config.localPort}`;
    els.tokenField.classList.toggle("hidden", !config.requiresToken);
    els.tokenInput.value = localStorage.getItem("fnssh.token") || "";
  } catch (error) {
    els.serverHint.textContent = "配置读取失败";
    showToast(error.message);
  }
}

function wireUI() {
  els.newSessionBtn.addEventListener("click", () => createSession());
  els.advancedToggle.addEventListener("click", toggleAdvancedPanel);
  els.hostInput.addEventListener("input", syncActiveForm);
  els.portInput.addEventListener("input", syncActiveForm);
  els.portInput.addEventListener("change", syncActiveForm);
  els.authSelect.addEventListener("change", toggleAuthFields);
  els.connectBtn.addEventListener("click", connectActive);
  els.disconnectBtn.addEventListener("click", disconnectActive);
  els.clearBtn.addEventListener("click", () => activeSession()?.term.clear());
  window.addEventListener("resize", () => fitActive(true));
}

function createSession() {
  const id = crypto.randomUUID ? crypto.randomUUID() : `s-${Date.now()}-${Math.random()}`;
  state.counter += 1;
  const title = `终端 ${state.counter}`;
  const pane = document.createElement("div");
  pane.className = "terminal-pane";
  pane.dataset.sessionId = id;
  const terminalFrame = document.createElement("div");
  terminalFrame.className = "terminal-frame";
  pane.appendChild(terminalFrame);
  els.terminalStack.appendChild(pane);

  const term = new Terminal({
    allowProposedApi: false,
    convertEol: true,
    cursorBlink: true,
    cursorStyle: "bar",
    fontFamily: '"Cascadia Mono", "JetBrains Mono", Consolas, monospace',
    fontSize: 14,
    letterSpacing: 0,
    lineHeight: 1.1,
    scrollback: 8000,
    theme: terminalTheme,
  });
  const fitAddon = new FitAddon.FitAddon();
  term.loadAddon(fitAddon);
  term.open(terminalFrame);
  term.writeln(`\x1b[38;5;114m${title}\x1b[0m`);
  term.onData((data) => {
    sendTerminalInput(sessionById(id), data);
  });
  term.attachCustomKeyEventHandler((event) => {
    if (event.type === "keydown" && (event.key === "Escape" || event.code === "Escape")) {
      const sent = sendTerminalInput(sessionById(id), "\x1b");
      if (sent) {
        event.preventDefault();
        event.stopPropagation();
        return false;
      }
    }
    return true;
  });

  const session = {
    id,
    index: state.counter,
    title,
    host: config.localHost,
    port: config.localPort,
    username: "",
    authType: "password",
    password: "",
    privateKey: "",
    passphrase: "",
    status: "idle",
    connected: false,
    ws: null,
    pane,
    term,
    fitAddon,
  };
  state.sessions.push(session);
  setActive(id);
  renderSessions();
  return session;
}

function setActive(id) {
  saveFormToActive();
  state.activeId = id;
  for (const session of state.sessions) {
    session.pane.classList.toggle("active", session.id === id);
  }
  loadActiveToForm();
  renderSessions();
  requestAnimationFrame(() => fitActive(true));
}

function activeSession() {
  return sessionById(state.activeId);
}

function sessionById(id) {
  return state.sessions.find((session) => session.id === id);
}

function sendTerminalInput(session, data) {
  if (!data || !session || !["connecting", "connected"].includes(session.status) || !session.ws || session.ws.readyState !== WebSocket.OPEN) {
    return false;
  }
  session.ws.send(JSON.stringify({ type: "input", data }));
  return true;
}

function renderSessions() {
  els.sessionList.innerHTML = "";
  for (const session of state.sessions) {
    const item = document.createElement("div");
    item.className = `session-item${session.id === state.activeId ? " active" : ""}`;
    item.role = "tab";
    item.tabIndex = 0;
    item.setAttribute("aria-selected", session.id === state.activeId ? "true" : "false");
    item.addEventListener("click", () => setActive(session.id));
    item.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        setActive(session.id);
      }
    });

    const dot = document.createElement("span");
    dot.className = `session-dot ${session.status}${session.connected ? " connected" : ""}`;

    const label = document.createElement("span");
    label.innerHTML = `<span class="session-name"></span><span class="session-meta"></span>`;
    label.querySelector(".session-name").textContent = session.title;
    label.querySelector(".session-meta").textContent = statusText(session);

    const close = document.createElement("button");
    close.className = "close-tab";
    close.type = "button";
    close.title = "关闭会话";
    close.innerHTML = "&times;";
    close.addEventListener("click", (event) => {
      event.stopPropagation();
      closeSession(session.id);
    });

    item.append(dot, label, close);
    els.sessionList.appendChild(item);
  }
}

function closeSession(id) {
  const index = state.sessions.findIndex((session) => session.id === id);
  if (index === -1) return;
  const [session] = state.sessions.splice(index, 1);
  if (session.ws) {
    session.ws.close();
  }
  session.term.dispose();
  session.pane.remove();
  if (state.sessions.length === 0) {
    createSession();
    return;
  }
  if (state.activeId === id) {
    setActive(state.sessions[Math.max(0, index - 1)].id);
  }
  renderSessions();
}

function saveFormToActive() {
  const session = activeSession();
  if (!session) return;
  session.host = els.hostInput.value.trim() || config.localHost;
  session.port = readPort();
  session.username = els.usernameInput.value.trim();
  session.authType = els.authSelect.value;
  session.password = els.passwordInput.value;
  session.privateKey = els.privateKeyInput.value;
  session.passphrase = els.passphraseInput.value;
  updateSessionHeader(session);
}

function loadActiveToForm() {
  const session = activeSession();
  if (!session) return;
  updateSessionHeader(session);
  els.hostInput.value = session.host || config.localHost;
  els.hostInput.disabled = session.connected;
  els.portInput.value = session.port || config.localPort;
  els.portInput.disabled = session.connected;
  els.usernameInput.value = session.username;
  els.authSelect.value = session.authType;
  els.passwordInput.value = session.password;
  els.privateKeyInput.value = session.privateKey;
  els.passphraseInput.value = session.passphrase;
  toggleAuthFields();
  paintStatus(session.status);
}

function syncActiveForm() {
  saveFormToActive();
  renderSessions();
}

function readPort() {
  const port = Number(els.portInput.value);
  if (Number.isInteger(port) && port > 0 && port <= 65535) {
    return port;
  }
  return config.localPort || 22;
}

function targetText(session) {
  return `${session.host || config.localHost}:${session.port || config.localPort}`;
}

function updateSessionHeader(session) {
  els.terminalTitle.textContent = session.title;
  els.serverHint.textContent = targetText(session);
}

function toggleAuthFields() {
  const useKey = els.authSelect.value === "key";
  els.passwordField.classList.toggle("hidden", useKey);
  els.keyField.classList.toggle("hidden", !useKey);
  els.passphraseField.classList.toggle("hidden", !useKey);
}

function toggleAdvancedPanel() {
  const expanded = els.advancedToggle.getAttribute("aria-expanded") === "true";
  setAdvancedPanel(!expanded);
}

function setAdvancedPanel(expanded) {
  els.advancedToggle.setAttribute("aria-expanded", expanded ? "true" : "false");
  els.advancedPanel.classList.toggle("collapsed", !expanded);
  const label = els.advancedToggle.querySelector("span");
  if (label) {
    label.textContent = expanded ? "收起" : "高级";
  }
  requestAnimationFrame(() => fitActive(true));
}

function connectActive() {
  saveFormToActive();
  const session = activeSession();
  if (!session || session.connected || session.status === "connecting") return;
  if (session.authType === "key" && !session.privateKey.trim()) {
    showToast("请填写私钥");
    return;
  }

  const token = els.tokenInput.value.trim();
  if (token) {
    localStorage.setItem("fnssh.token", token);
  }

  const wsURL = new URL("/ws", window.location.href);
  wsURL.protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  if (token) wsURL.searchParams.set("token", token);

  session.ws = new WebSocket(wsURL);
  session.status = "connecting";
  session.term.writeln("\r\n\x1b[38;5;221mConnecting...\x1b[0m");
  paintStatus("connecting");
  renderSessions();

  session.ws.addEventListener("open", () => {
    fitActive(false);
    session.ws.send(JSON.stringify({
      type: "connect",
      host: session.host || config.localHost,
      port: session.port || config.localPort,
      username: session.username,
      password: session.authType === "password" ? session.password : "",
      privateKey: session.authType === "key" ? session.privateKey : "",
      passphrase: session.authType === "key" ? session.passphrase : "",
      cols: session.term.cols,
      rows: session.term.rows,
    }));
  });

  session.ws.addEventListener("message", (event) => handleServerMessage(session, event));
  session.ws.addEventListener("close", () => {
    session.connected = false;
    if (session.status !== "error") {
      session.status = "idle";
    }
    if (activeSession()?.id === session.id) paintStatus(session.status);
    renderSessions();
  });
  session.ws.addEventListener("error", () => {
    session.status = "error";
    session.connected = false;
    if (activeSession()?.id === session.id) paintStatus("error");
    showToast("WebSocket 连接失败");
    renderSessions();
  });
}

function handleServerMessage(session, event) {
  let msg;
  try {
    msg = JSON.parse(event.data);
  } catch {
    return;
  }
  if (msg.type === "output") {
    session.term.write(msg.data || "");
    return;
  }
  if (msg.type === "status") {
    session.connected = msg.state === "connected";
    session.status = session.connected ? "connected" : "idle";
    session.term.writeln(`\r\n\x1b[38;5;114m${msg.message || msg.state}\x1b[0m`);
    if (activeSession()?.id === session.id) paintStatus(session.status);
    renderSessions();
    return;
  }
  if (msg.type === "error") {
    session.status = "error";
    session.connected = false;
    session.term.writeln(`\r\n\x1b[38;5;203m${msg.message || "连接失败"}\x1b[0m`);
    if (session.ws) session.ws.close();
    if (activeSession()?.id === session.id) paintStatus("error");
    showToast(msg.message || "连接失败");
    renderSessions();
  }
}

function disconnectActive() {
  const session = activeSession();
  if (!session) return;
  if (session.ws && session.ws.readyState === WebSocket.OPEN) {
    session.ws.send(JSON.stringify({ type: "close" }));
  }
  if (session.ws) {
    session.ws.close();
  }
  session.connected = false;
  session.status = "idle";
  paintStatus("idle");
  renderSessions();
}

function fitActive(sendResize) {
  const session = activeSession();
  if (!session) return;
  try {
    const dimensions = session.fitAddon.proposeDimensions?.();
    if (dimensions) {
      session.term.resize(dimensions.cols, Math.max(5, dimensions.rows - terminalRowGuard));
    } else {
      session.fitAddon.fit();
    }
    if (sendResize && session.connected && session.ws?.readyState === WebSocket.OPEN) {
      session.ws.send(JSON.stringify({
        type: "resize",
        cols: session.term.cols,
        rows: session.term.rows,
      }));
    }
  } catch {
    // xterm-fit can throw while the pane is hidden during layout transitions.
  }
}

function paintStatus(status) {
  els.connectBtn.classList.remove(
    "connection-status-idle",
    "connection-status-connecting",
    "connection-status-connected",
    "connection-status-error",
  );
  els.connectBtn.classList.add(`connection-status-${status}`);
  els.connectBtn.setAttribute("aria-busy", status === "connecting" ? "true" : "false");
  const label = els.connectBtn.querySelector("span");
  if (status === "connected") {
    label.textContent = "已连接";
  } else if (status === "connecting") {
    label.textContent = "连接中";
  } else if (status === "error") {
    label.textContent = "错误";
  } else {
    label.textContent = "连接";
  }
}

function statusText(session) {
  if (session.connected) return `已连接 ${targetText(session)}`;
  if (session.status === "connecting") return `连接中 ${targetText(session)}`;
  if (session.status === "error") return "错误";
  return targetText(session);
}

let toastTimer = 0;
function showToast(message) {
  window.clearTimeout(toastTimer);
  els.toast.textContent = message;
  els.toast.classList.add("show");
  toastTimer = window.setTimeout(() => els.toast.classList.remove("show"), 3400);
}
