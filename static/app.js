let selfUser = null;
let selfID = null;
let selfPass = null;

const conversations = {}; // key -> {type, title, peerName, peerID, groupID, lastRead, lastKnown, unread}
let activeKey = null;
let pollTimer = null;

const chatListEl = document.getElementById('chatList');
const sideStatus = document.getElementById('sideStatus');
const whoamiEl = document.getElementById('whoami');

const chatTitle = document.getElementById('chatTitle');
const chatSubtitle = document.getElementById('chatSubtitle');
const idsInfo = document.getElementById('idsInfo');

const messagesEl = document.getElementById('messages');
const msgInput = document.getElementById('msgInput');
const sendBtn = document.getElementById('sendBtn');

const attachBtn = document.getElementById('attachBtn');
const fileInput = document.getElementById('fileInput');

const peerSearch = document.getElementById('peerSearch');
const openByNameBtn = document.getElementById('openByNameBtn');
const createGroupBtn = document.getElementById('createGroupBtn');
const refreshBtn = document.getElementById('refreshBtn');
const logoutBtn = document.getElementById('logoutBtn');

const addMemberBtn = document.getElementById('addMemberBtn');

const onlineListEl = document.getElementById('onlineList');
const onlineCountEl = document.getElementById('onlineCount');

const ONLINE_WINDOW_SEC = 15;

// id -> username cache (чтобы хоть как-то подписывать сообщения в беседах)
const idToName = new Map();

function setStatus(text, ok) {
  sideStatus.textContent = text || '';
  sideStatus.className = 'status';
  if (!text) return;
  if (ok) sideStatus.classList.add('ok');
  else sideStatus.classList.add('error');
}

async function apiJSON(url, method, body) {
  const opts = { method: method || 'GET' };
  if (body) {
    opts.headers = { 'Content-Type': 'application/json' };
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(url, opts);
  const ct = resp.headers.get('content-type') || '';
  const text = await resp.text().catch(() => '');
  if (!resp.ok) throw new Error(resp.status + ' ' + text);
  if (resp.status === 204) return null;
  if (ct.includes('application/json')) {
    try { return JSON.parse(text); } catch (_) { return null; }
  }
  return text;
}

// ===== state per-user (fix: “вижу переписки других аккаунтов”) =====
function stateKey() { return selfUser ? `ss_state_${selfUser}` : 'ss_state_unknown'; }

function saveState() {
  if (!selfUser) return;
  const data = { conversations, activeKey };
  try { localStorage.setItem(stateKey(), JSON.stringify(data)); } catch (_) {}
}

function loadState() {
  if (!selfUser) return;
  try {
    const raw = localStorage.getItem(stateKey());
    if (!raw) return;
    const data = JSON.parse(raw);
    if (data && data.conversations) {
      for (const k of Object.keys(data.conversations)) {
        conversations[k] = data.conversations[k];
      }
    }
    if (data && data.activeKey) activeKey = data.activeKey;
  } catch (_) {}
}

function clearAllState() {
  for (const k of Object.keys(conversations)) delete conversations[k];
  activeKey = null;
  saveState();
}

// ===== auth (no login window inside app) =====
function readSessionCreds() {
  try {
    const u = sessionStorage.getItem('ss_username');
    const p = sessionStorage.getItem('ss_password');
    return { u, p };
  } catch (_) {
    return { u: null, p: null };
  }
}

async function ensureLogin() {
  if (selfID) return;

  const { u, p } = readSessionCreds();
  if (!u || !p) {
    // нет сессии -> на login.html
    window.location.href = '/login.html';
    throw new Error('no session');
  }

  selfUser = (u || '').trim();
  selfPass = p;

  const login = await apiJSON('/login', 'POST', { username: selfUser, password: selfPass });
  selfID = login.id;

  whoamiEl.textContent = `${selfUser} · id=${selfID}`;
  idToName.set(selfID, selfUser);

  setStatus('Вход выполнен', true);

  // загрузить локальный state под этим юзером
  clearAllUI();
  loadState();

  await bootstrapConversations();
  renderChatList();

  if (activeKey && conversations[activeKey]) {
    await setActiveConversation(activeKey);
  }

  startPolling();
}

function clearAllUI() {
  chatTitle.textContent = 'Чат не выбран';
  chatSubtitle.textContent = 'Открой диалог по нику или выбери чат слева';
  idsInfo.textContent = '';
  messagesEl.innerHTML = '';
  msgInput.value = '';
  msgInput.disabled = true;
  sendBtn.disabled = true;
  if (attachBtn) attachBtn.disabled = true;
  if (addMemberBtn) addMemberBtn.style.display = 'none';
}

function logout() {
  try {
    sessionStorage.removeItem('ss_username');
    sessionStorage.removeItem('ss_password');
  } catch (_) {}
  window.location.href = '/login.html';
}

// ===== UI helpers =====
function convKeyUser(peerName) { return 'u:' + peerName; }
function convKeyGroup(groupID) { return 'g:' + groupID; }

function avatarLetter(title) {
  const s = (title || '').trim();
  return s ? s[0].toUpperCase() : '?';
}

function renderChatList() {
  chatListEl.innerHTML = '';

  const keys = Object.keys(conversations);
  keys.sort((a, b) => {
    const A = conversations[a], B = conversations[b];
    const au = A.unread || 0, bu = B.unread || 0;
    if (au !== bu) return bu - au;
    return (A.title || '').localeCompare(B.title || '');
  });

  for (const key of keys) {
    const conv = conversations[key];

    const item = document.createElement('div');
    item.className = 'chat-item' + (key === activeKey ? ' active' : '');

    const av = document.createElement('div');
    av.className = 'chat-avatar';
    av.textContent = avatarLetter(conv.title);

    const text = document.createElement('div');
    text.className = 'chat-text';

    const title = document.createElement('div');
    title.className = 'chat-title';
    title.textContent = conv.title;

    const sub = document.createElement('div');
    sub.className = 'chat-sub';
    sub.textContent = conv.type === 'group' ? 'Групповая беседа' : 'Личный диалог';

    text.appendChild(title);
    text.appendChild(sub);

    item.appendChild(av);
    item.appendChild(text);

    if ((conv.unread || 0) > 0) {
      const badge = document.createElement('div');
      badge.className = 'badge';
      badge.textContent = String(conv.unread);
      item.appendChild(badge);
    }

    item.addEventListener('click', () => setActiveConversation(key).catch(err => setStatus(err.message, false)));
    chatListEl.appendChild(item);
  }
}

function renderMessageBody(container, text) {
  const s = String(text || '');
  const m = s.match(/^\[\[img:(\d+)\]\]$/);
  if (m) {
    const id = m[1];
    const img = document.createElement('img');
    img.src = '/api/plain_media/get?id=' + encodeURIComponent(id);
    img.loading = 'lazy';
    img.className = 'msg-img';
    container.appendChild(img);
    return;
  }
  container.textContent = s;
}

function nameByID(id) {
  if (idToName.has(id)) return idToName.get(id);
  return 'user#' + id;
}

function renderMessages(msgs, conv) {
  messagesEl.innerHTML = '';
  if (!Array.isArray(msgs)) return 0;

  let maxID = conv?.lastKnown || 0;

  for (const m of msgs) {
    const mid = (typeof m.id === 'number' ? m.id : 0);
    if (mid > maxID) maxID = mid;

    const wrapper = document.createElement('div');
    const meta = document.createElement('div');
    const body = document.createElement('div');

    const isSelf = (m.from_user_id === selfID);
    wrapper.className = 'msg ' + (isSelf ? 'self' : 'peer');
    meta.className = 'msg-meta';

    // group может не давать from_username — используем кэш по id
    const who =
      (m.from_username && String(m.from_username).trim())
        ? m.from_username
        : (isSelf ? 'Ты' : nameByID(m.from_user_id));

    meta.textContent = m.created_at ? (who + ' · ' + m.created_at) : who;

    renderMessageBody(body, m.text);

    wrapper.appendChild(meta);
    wrapper.appendChild(body);
    messagesEl.appendChild(wrapper);
  }

  messagesEl.scrollTop = messagesEl.scrollHeight;
  return maxID;
}

// ===== bootstrap: inbox + groups =====
async function bootstrapConversations() {
  await ensureLogin();

  // 1) direct inbox
  try {
    const inbox = await apiJSON('/chat/inbox?user_id=' + encodeURIComponent(selfID), 'GET');
    if (Array.isArray(inbox)) {
      for (const it of inbox) {
        const peerName = String(it.peer_username || '').trim();
        if (!peerName) continue;
        const key = convKeyUser(peerName);

        idToName.set(it.peer_id, peerName);

        if (!conversations[key]) {
          conversations[key] = {
            type: 'user',
            title: peerName,
            peerName,
            peerID: it.peer_id,
            lastRead: 0,
            lastKnown: it.last_message_id || 0,
            unread: 0
          };
        } else {
          conversations[key].peerID = conversations[key].peerID || it.peer_id;
          conversations[key].lastKnown = Math.max(conversations[key].lastKnown || 0, it.last_message_id || 0);
        }
      }
    }
  } catch (_) {}

  // 2) groups
  try {
    const list = await apiJSON('/groups/by_user?user_id=' + encodeURIComponent(selfID), 'GET');
    if (Array.isArray(list)) {
      for (const g of list) {
        const key = convKeyGroup(g.id);
        if (!conversations[key]) {
          conversations[key] = {
            type: 'group',
            title: g.name || ('group #' + g.id),
            groupID: g.id,
            lastRead: 0,
            lastKnown: 0,
            unread: 0
          };
        } else {
          conversations[key].groupID = g.id;
          conversations[key].title = conversations[key].title || g.name || ('group #' + g.id);
        }
      }
    }
  } catch (_) {}

  // initial: ставим lastRead=lastKnown чтобы историю не считать непрочитанной
  for (const key of Object.keys(conversations)) {
    const c = conversations[key];
    if (!c.lastRead) c.lastRead = c.lastKnown || 0;
    if (!c.lastKnown) c.lastKnown = c.lastRead || 0;
    c.unread = 0;
  }

  saveState();
}

// ===== data fetch =====
async function fetchDirectMessages(conv) {
  if (!conv.peerID) {
    const data = await apiJSON('/public_key?username=' + encodeURIComponent(conv.peerName), 'GET');
    conv.peerID = data.id;
    idToName.set(conv.peerID, conv.peerName);
  }
  const msgs = await apiJSON('/chat/messages?user_a=' + encodeURIComponent(selfID) + '&user_b=' + encodeURIComponent(conv.peerID), 'GET');
  return Array.isArray(msgs) ? msgs : [];
}

async function fetchGroupMessages(conv) {
  const msgs = await apiJSON('/groups/messages?group_id=' + encodeURIComponent(conv.groupID), 'GET');
  return Array.isArray(msgs) ? msgs : [];
}

// ===== unread calculation =====
function recomputeUnreadFromMsgs(conv, msgs) {
  const lastRead = conv.lastRead || 0;
  let maxID = conv.lastKnown || 0;
  let unread = 0;

  for (const m of msgs) {
    const mid = m.id || 0;
    if (mid > maxID) maxID = mid;

    if (mid > lastRead) {
      if (conv.type === 'user') {
        if (m.to_user_id === selfID) unread++;
      } else {
        // group: всё что не от себя
        if (m.from_user_id !== selfID) unread++;
      }
    }
  }

  conv.lastKnown = maxID;
  conv.unread = Math.max(0, unread);
}

// ===== polling =====
function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

function startPolling() {
  stopPolling();
  pollTimer = setInterval(() => {
    pollOnce().catch(() => {});
  }, 1500);
}

async function pollOnce() {
  if (!selfID) return;

  // presence
  presencePing().catch(() => {});
  if (!pollOnce._tOnline || Date.now() - pollOnce._tOnline > 3000) {
    pollOnce._tOnline = Date.now();
    presenceFetchOnline().catch(() => {});
  }

  // обновить списки чатов из inbox/groups (чтобы появлялись новые диалоги и новые группы)
  if (!pollOnce._tBoot || Date.now() - pollOnce._tBoot > 5000) {
    pollOnce._tBoot = Date.now();
    await bootstrapConversations().catch(() => {});
  }

  // пересчитать unread по всем чатам
  const keys = Object.keys(conversations);
  for (const key of keys) {
    const conv = conversations[key];

    let msgs = [];
    try {
      msgs = (conv.type === 'group') ? await fetchGroupMessages(conv) : await fetchDirectMessages(conv);
    } catch (_) {
      continue;
    }

    recomputeUnreadFromMsgs(conv, msgs);

    // если активный чат открыт — помечаем прочитанным и рендерим
    if (key === activeKey) {
      conv.lastRead = conv.lastKnown;
      conv.unread = 0;
      renderMessages(msgs, conv);
    }
  }

  renderChatList();
  saveState();
}

// ===== actions =====
async function setActiveConversation(key) {
  await ensureLogin();

  activeKey = key;
  const conv = conversations[key];
  if (!conv) return;

  if (conv.type === 'group') {
    chatTitle.textContent = conv.title;
    chatSubtitle.textContent = 'Групповая беседа';
    idsInfo.textContent = `Ты: ${selfID} · group_id: ${conv.groupID}`;
    addMemberBtn.style.display = 'inline-flex';
  } else {
    chatTitle.textContent = 'Диалог с ' + conv.title;
    chatSubtitle.textContent = 'Личный чат';
    idsInfo.textContent = `Ты: ${selfID} · peer: ${conv.peerName}`;
    addMemberBtn.style.display = 'none';
  }

  msgInput.disabled = false;
  sendBtn.disabled = false;
  if (attachBtn) attachBtn.disabled = false;

  let msgs = [];
  msgs = (conv.type === 'group') ? await fetchGroupMessages(conv) : await fetchDirectMessages(conv);

  recomputeUnreadFromMsgs(conv, msgs);
  conv.lastRead = conv.lastKnown;
  conv.unread = 0;

  renderMessages(msgs, conv);
  renderChatList();
  saveState();
}

// ===== send text =====
async function sendMessage() {
  const text = msgInput.value.trim();
  if (!text || !activeKey) return;

  const conv = conversations[activeKey];
  if (!conv) return;

  await ensureLogin();

  if (conv.type === 'group') {
    await apiJSON('/groups/send', 'POST', { group_id: conv.groupID, from_user_id: selfID, text });
  } else {
    if (!conv.peerID) {
      const data = await apiJSON('/public_key?username=' + encodeURIComponent(conv.peerName), 'GET');
      conv.peerID = data.id;
      idToName.set(conv.peerID, conv.peerName);
    }
    await apiJSON('/chat/send', 'POST', { from_user_id: selfID, to_user_id: conv.peerID, text });
  }

  msgInput.value = '';
  await pollOnce();
}

// ===== create group =====
async function createGroup() {
  await ensureLogin();

  const name = prompt('Название беседы:', 'My group');
  if (!name) return;

  const resp = await apiJSON('/groups/create', 'POST', {
    name: name,
    owner_id: selfID,
    member_ids: []
  });

  const gid = resp.id;
  const key = convKeyGroup(gid);
  conversations[key] = {
    type: 'group',
    title: resp.name || name,
    groupID: gid,
    lastRead: 0,
    lastKnown: 0,
    unread: 0
  };

  renderChatList();
  saveState();
  await setActiveConversation(key);
}

// ===== add member to group =====
async function addMemberToActiveGroup() {
  if (!activeKey) return;
  const conv = conversations[activeKey];
  if (!conv || conv.type !== 'group') return;

  const username = prompt('Кого добавить? (username)', '');
  if (!username) return;

  const pk = await apiJSON('/public_key?username=' + encodeURIComponent(username.trim()), 'GET');
  const uid = pk.id;

  await apiJSON('/groups/add_member', 'POST', { group_id: conv.groupID, user_id: uid });
  setStatus(`Добавлен: ${username} (id=${uid})`, true);
}

// ===== open direct by name (search/open dialog) =====
async function openDirectByName() {
  await ensureLogin();

  const peerName = (peerSearch.value || '').trim();
  if (!peerName) {
    setStatus('Введи username', false);
    return;
  }
  if (peerName === selfUser) {
    setStatus('Это ты сам', false);
    return;
  }

  const key = convKeyUser(peerName);
  if (!conversations[key]) {
    conversations[key] = {
      type: 'user',
      title: peerName,
      peerName,
      peerID: null,
      lastRead: 0,
      lastKnown: 0,
      unread: 0
    };
  }
  renderChatList();
  saveState();
  await setActiveConversation(key);
}

// ===== attach photo (plain_media encrypted server-side key) =====
if (attachBtn && fileInput) {
  attachBtn.addEventListener('click', () => {
    if (!attachBtn.disabled) fileInput.click();
  });

  fileInput.addEventListener('change', () => {
    uploadSelectedImage().catch(err => setStatus('Ошибка фото: ' + err.message, false));
  });
}

async function uploadSelectedImage() {
  if (!activeKey) return;
  const conv = conversations[activeKey];
  if (!conv) return;
  await ensureLogin();

  const f = fileInput.files && fileInput.files[0];
  if (!f) return;

  const fd = new FormData();
  fd.append('file', f);

  if (conv.type === 'group') {
    fd.append('kind', 'group');
    fd.append('from_user_id', String(selfID));
    fd.append('group_id', String(conv.groupID));
  } else {
    if (!conv.peerID) {
      const data = await apiJSON('/public_key?username=' + encodeURIComponent(conv.peerName), 'GET');
      conv.peerID = data.id;
      idToName.set(conv.peerID, conv.peerName);
    }
    fd.append('kind', 'direct');
    fd.append('from_user_id', String(selfID));
    fd.append('to_user_id', String(conv.peerID));
  }

  const resp = await fetch('/api/plain_media/upload', { method: 'POST', body: fd });
  const text = await resp.text().catch(() => '');
  if (!resp.ok) throw new Error('upload failed: ' + resp.status + ' ' + text);

  const data = JSON.parse(text);
  const tag = `[[img:${data.id}]]`;

  // отправляем тэг как текст
  if (conv.type === 'group') {
    await apiJSON('/groups/send', 'POST', { group_id: conv.groupID, from_user_id: selfID, text: tag });
  } else {
    await apiJSON('/chat/send', 'POST', { from_user_id: selfID, to_user_id: conv.peerID, text: tag });
  }

  fileInput.value = '';
  await pollOnce();
}

// ===== presence =====
async function presencePing() {
  if (!selfID) return;
  await apiJSON('/presence/ping', 'POST', { user_id: selfID });
}

function renderOnline(users) {
  if (!onlineListEl) return;

  const list = Array.isArray(users) ? users : [];

  for (const u of list) {
    if (u && u.id && u.username) idToName.set(u.id, u.username);
  }

  onlineListEl.innerHTML = '';

  const filtered = list.filter(u => u && u.id && u.username && u.id !== selfID);
  if (onlineCountEl) onlineCountEl.textContent = filtered.length ? String(filtered.length) : '';

  if (!filtered.length) {
    const d = document.createElement('div');
    d.style.fontSize = '12px';
    d.style.color = 'var(--muted)';
    d.textContent = 'Никого нет онлайн';
    onlineListEl.appendChild(d);
    return;
  }

  for (const u of filtered) {
    const row = document.createElement('div');
    row.className = 'chat-item';
    row.style.margin = '0';
    row.style.cursor = 'pointer';

    const av = document.createElement('div');
    av.className = 'chat-avatar';
    av.textContent = (u.username || '?')[0]?.toUpperCase?.() || '?';

    const text = document.createElement('div');
    text.className = 'chat-text';

    const title = document.createElement('div');
    title.className = 'chat-title';
    title.textContent = u.username;

    const sub = document.createElement('div');
    sub.className = 'chat-sub';
    sub.textContent = 'online';

    text.appendChild(title);
    text.appendChild(sub);

    row.appendChild(av);
    row.appendChild(text);

    row.addEventListener('click', async () => {
      peerSearch.value = u.username;
      await openDirectByName();
    });

    onlineListEl.appendChild(row);
  }
}

async function presenceFetchOnline() {
  const users = await apiJSON('/presence/online', 'GET');
  renderOnline(users);
}

// ===== events =====
logoutBtn.addEventListener('click', logout);

openByNameBtn.addEventListener('click', () => {
  openDirectByName().catch(err => setStatus(err.message, false));
});

peerSearch.addEventListener('keydown', (e) => {
  if (e.key === 'Enter') {
    e.preventDefault();
    openByNameBtn.click();
  }
});

sendBtn.addEventListener('click', () => {
  sendMessage().catch(err => setStatus('Ошибка отправки: ' + err.message, false));
});

msgInput.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    sendBtn.click();
  }
});

createGroupBtn.addEventListener('click', () => {
  createGroup().catch(err => setStatus('Ошибка беседы: ' + err.message, false));
});

refreshBtn.addEventListener('click', () => {
  pollOnce().catch(err => setStatus('Ошибка обновления: ' + err.message, false));
});

addMemberBtn.addEventListener('click', () => {
  addMemberToActiveGroup().catch(err => setStatus('Ошибка добавления: ' + err.message, false));
});

// старт
ensureLogin().catch(() => {});