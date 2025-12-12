let selfUser = null;
let selfPass = null;
let selfID = null;

// conversations: key -> {type, title, peerName, peerID, groupID, lastRead, unread, lastPreview?}
const conversations = {};
let activeKey = null;

let pollTimer = null;

const chatListEl = document.getElementById('chatList');
const sideStatus = document.getElementById('sideStatus');
const whoami = document.getElementById('whoami');

const chatTitle = document.getElementById('chatTitle');
const chatSubtitle = document.getElementById('chatSubtitle');
const idsInfo = document.getElementById('idsInfo');

const messagesEl = document.getElementById('messages');
const msgInput = document.getElementById('msgInput');
const sendBtn = document.getElementById('sendBtn');

const attachBtn = document.getElementById('attachBtn');
const fileInput = document.getElementById('fileInput');

const openDirectBtn = document.getElementById('openDirectBtn');
const createGroupBtn = document.getElementById('createGroupBtn');

function setStatus(text, ok) {
  sideStatus.textContent = text || '';
  sideStatus.className = 'status';
  if (!text) return;
  if (ok) sideStatus.classList.add('ok');
  else sideStatus.classList.add('error');
}

async function apiJSON(url, method, body) {
  const opts = { method: method || 'GET' };
  if (body !== undefined) {
    opts.headers = { 'Content-Type': 'application/json' };
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(url, opts);
  if (!resp.ok) {
    const t = await resp.text().catch(() => '');
    throw new Error(resp.status + ' ' + (t || ''));
  }
  if (resp.status === 204) return null;
  const ct = resp.headers.get('content-type') || '';
  if (ct.includes('application/json')) return await resp.json();
  return await resp.text();
}

// ===== session/state (не мешаем аккаунты) =====
function stateKey() {
  // пока selfUser ещё не выставлен — fallback
  const u = selfUser || sessionStorage.getItem('ss_username') || 'anon';
  return 'ss_app_state:' + u;
}

function saveState() {
  try {
    const snapshot = {
      activeKey,
      conversations: Object.fromEntries(Object.entries(conversations).map(([k, v]) => [k, {
        type: v.type,
        title: v.title,
        peerName: v.peerName || null,
        peerID: v.peerID || null,
        groupID: v.groupID || null,
        lastRead: v.lastRead || 0
      }]))
    };
    sessionStorage.setItem(stateKey(), JSON.stringify(snapshot));
  } catch (_) {}
}

function loadState() {
  try {
    const raw = sessionStorage.getItem(stateKey());
    if (!raw) return;
    const st = JSON.parse(raw);
    if (st && st.conversations) {
      for (const [k, v] of Object.entries(st.conversations)) {
        conversations[k] = {
          type: v.type,
          title: v.title,
          peerName: v.peerName,
          peerID: v.peerID,
          groupID: v.groupID,
          lastRead: v.lastRead || 0,
          unread: 0
        };
      }
    }
    if (st && st.activeKey) activeKey = st.activeKey;
  } catch (_) {}
}

// ===== auth =====
async function ensureLogin() {
  if (selfID) return;

  const u = (sessionStorage.getItem('ss_username') || '').trim();
  const p = (sessionStorage.getItem('ss_password') || '');

  if (!u || !p) {
    window.location.href = '/login.html';
    throw new Error('no session');
  }

  selfUser = u;
  selfPass = p;

  // регистрируем (если уже есть — ок)
  try { await apiJSON('/register', 'POST', { username: u, password: p }); } catch (_) {}

  const login = await apiJSON('/login', 'POST', { username: u, password: p });
  selfID = login.id;

  whoami.textContent = selfUser + ' · id=' + selfID;
  setStatus('Ок: ' + selfUser, true);

  loadState();

  // подтянуть “инбокс” (диалоги, которые есть у собеседников, даже если ты их не открывал)
  await refreshInbox();
  await loadGroups();

  // если есть активный чат из прошлой сессии — восстановим
  renderChatList();
  if (activeKey && conversations[activeKey]) {
    await setActiveConversation(activeKey, { silent: true });
  }

  startPolling();
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

    item.addEventListener('click', () => {
      setActiveConversation(key).catch(err => setStatus(err.message, false));
    });
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

function renderMessages(msgs) {
  messagesEl.innerHTML = '';
  if (!Array.isArray(msgs)) return;

  for (const m of msgs) {
    const wrapper = document.createElement('div');
    const meta = document.createElement('div');
    const body = document.createElement('div');

    const isSelf = (m.from_user_id === selfID);
    wrapper.className = 'msg ' + (isSelf ? 'self' : 'peer');
    meta.className = 'msg-meta';

    const who = (m.from_username && String(m.from_username).trim())
      ? m.from_username
      : (isSelf ? 'Ты' : 'Собеседник');

    meta.textContent = m.created_at ? (who + ' · ' + m.created_at) : who;
    renderMessageBody(body, m.text);

    wrapper.appendChild(meta);
    wrapper.appendChild(body);
    messagesEl.appendChild(wrapper);
  }
  messagesEl.scrollTop = messagesEl.scrollHeight;
}

// ===== data load =====
async function loadGroups() {
  if (!selfID) return;
  const list = await apiJSON('/groups/by_user?user_id=' + encodeURIComponent(selfID), 'GET');
  if (!Array.isArray(list)) return;

  for (const g of list) {
    const key = convKeyGroup(g.id);
    if (!conversations[key]) {
      conversations[key] = {
        type: 'group',
        title: g.name || ('group #' + g.id),
        groupID: g.id,
        lastRead: 0,
        unread: 0
      };
    } else {
      conversations[key].type = 'group';
      conversations[key].groupID = g.id;
      conversations[key].title = g.name || conversations[key].title;
    }
  }
}

async function refreshInbox() {
  if (!selfID) return;
  // если эндпоинта нет — просто молча пропускаем
  let list = null;
  try {
    list = await apiJSON('/chat/inbox?user_id=' + encodeURIComponent(selfID), 'GET');
  } catch (_) {
    return;
  }
  if (!Array.isArray(list)) return;

  for (const it of list) {
    const peerName = it.peer_username;
    if (!peerName) continue;
    const key = convKeyUser(peerName);
    if (!conversations[key]) {
      conversations[key] = {
        type: 'user',
        title: peerName,
        peerName,
        peerID: it.peer_id || null,
        lastRead: 0,
        unread: 0
      };
    } else {
      conversations[key].type = 'user';
      conversations[key].peerName = peerName;
      if (!conversations[key].peerID && it.peer_id) conversations[key].peerID = it.peer_id;
    }
  }
}

async function fetchMessagesForConv(conv) {
  if (conv.type === 'group') {
    return await apiJSON('/groups/messages?group_id=' + encodeURIComponent(conv.groupID), 'GET');
  }
  if (!conv.peerID) {
    const data = await apiJSON('/public_key?username=' + encodeURIComponent(conv.peerName), 'GET');
    conv.peerID = data.id;
  }
  return await apiJSON(
    '/chat/messages?user_a=' + encodeURIComponent(selfID) + '&user_b=' + encodeURIComponent(conv.peerID),
    'GET'
  );
}

// точный подсчёт непрочитанных: считаем только чужие сообщения после lastRead
function computeUnread(conv, msgs) {
  const base = conv.lastRead || 0;
  let c = 0;

  for (const m of (msgs || [])) {
    const mid = m.id || 0;
    if (mid <= base) continue;

    if (conv.type === 'group') {
      if ((m.from_user_id || 0) !== selfID) c++;
    } else {
      if ((m.to_user_id || 0) === selfID) c++;
    }
  }
  return c;
}

function maxID(msgs) {
  let mx = 0;
  for (const m of (msgs || [])) mx = Math.max(mx, m.id || 0);
  return mx;
}

async function loadMessagesForActive({ markRead = true } = {}) {
  if (!activeKey) return;
  const conv = conversations[activeKey];
  if (!conv) return;

  const msgs = await fetchMessagesForConv(conv);
  renderMessages(msgs);

  if (markRead) {
    conv.lastRead = maxID(msgs);
    conv.unread = 0;
    saveState();
    renderChatList();
  }
}

// ===== polling =====
function stopPolling() {
  if (pollTimer) clearInterval(pollTimer);
  pollTimer = null;
}

function startPolling() {
  stopPolling();
  pollTimer = setInterval(() => {
    pollOnce().catch(() => {});
  }, 1500);
}

async function pollOnce() {
  if (!selfID) return;

  // подтягиваем инбокс+группы иногда (чтобы новые диалоги/группы появлялись)
  // не каждый тик, чтобы не спамить сервер
  const now = Date.now();
  if (!pollOnce._t0 || now - pollOnce._t0 > 6000) {
    pollOnce._t0 = now;
    await refreshInbox();
    await loadGroups();
  }

  const keys = Object.keys(conversations);
  for (const key of keys) {
    const conv = conversations[key];

    let msgs = [];
    try {
      msgs = await fetchMessagesForConv(conv);
    } catch (_) {
      continue;
    }

    // 1) активный чат: если появились новые — сразу перерисовать, и сразу считать прочитанным
    if (key === activeKey) {
      const prevRead = conv.lastRead || 0;
      const mx = maxID(msgs);

      if (mx > prevRead) {
        renderMessages(msgs);
        conv.lastRead = mx;
        conv.unread = 0;
        saveState();
      } else {
        conv.unread = 0;
      }
      continue;
    }

    // 2) неактивные: считаем unread точно
    conv.unread = computeUnread(conv, msgs);
  }

  renderChatList();
  saveState();
}

// ===== actions =====
async function setActiveConversation(key, { silent = false } = {}) {
  await ensureLogin();

  activeKey = key;
  const conv = conversations[key];
  if (!conv) return;

  if (conv.type === 'group') {
    chatTitle.textContent = conv.title;
    chatSubtitle.textContent = 'Групповая беседа';
    idsInfo.textContent = 'Ты: ' + selfID + ' · group_id: ' + conv.groupID;
  } else {
    chatTitle.textContent = 'Диалог с ' + conv.title;
    chatSubtitle.textContent = 'Личный чат';
    idsInfo.textContent = 'Ты: ' + selfID + ' · peer: ' + conv.peerName;
  }

  msgInput.disabled = false;
  sendBtn.disabled = false;
  if (attachBtn) attachBtn.disabled = false;

  renderChatList();
  saveState();

  // при открытии чата — сразу рисуем и отмечаем прочитанным
  await loadMessagesForActive({ markRead: true });

  if (!silent) setStatus('', true);
}

// ===== отправка =====
async function sendMessage() {
  const text = msgInput.value.trim();
  if (!text || !activeKey) return;

  const conv = conversations[activeKey];
  if (!conv) return;

  await ensureLogin();

  if (conv.type === 'group') {
    await apiJSON('/groups/send', 'POST', {
      group_id: conv.groupID,
      from_user_id: selfID,
      text
    });
  } else {
    if (!conv.peerID) {
      const data = await apiJSON('/public_key?username=' + encodeURIComponent(conv.peerName), 'GET');
      conv.peerID = data.id;
    }
    await apiJSON('/chat/send', 'POST', {
      from_user_id: selfID,
      to_user_id: conv.peerID,
      text
    });
  }

  msgInput.value = '';
  await loadMessagesForActive({ markRead: true });
}

// ===== direct/group create =====
async function openDirectChat() {
  await ensureLogin();
  const peer = prompt('Username собеседника (например: bob):', 'bob');
  if (!peer) return;

  const peerName = peer.trim();
  if (!peerName) return;

  const key = convKeyUser(peerName);
  if (!conversations[key]) {
    conversations[key] = {
      type: 'user',
      title: peerName,
      peerName,
      peerID: null,
      lastRead: 0,
      unread: 0
    };
  }

  renderChatList();
  saveState();
  await setActiveConversation(key);
}

async function createGroup() {
  await ensureLogin();
  const name = prompt('Название беседы:', 'My group');
  if (!name) return;

  const resp = await apiJSON('/groups/create', 'POST', {
    name,
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
    unread: 0
  };

  renderChatList();
  saveState();
  await setActiveConversation(key);
}

// ===== attach photo =====
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
    }
    fd.append('kind', 'direct');
    fd.append('from_user_id', String(selfID));
    fd.append('to_user_id', String(conv.peerID));
  }

  const resp = await fetch('/api/plain_media/upload', { method: 'POST', body: fd });
  if (!resp.ok) {
    const t = await resp.text().catch(() => '');
    throw new Error('upload failed: ' + resp.status + ' ' + t);
  }
  const data = await resp.json();
  const tag = `[[img:${data.id}]]`;

  if (conv.type === 'group') {
    await apiJSON('/groups/send', 'POST', { group_id: conv.groupID, from_user_id: selfID, text: tag });
  } else {
    await apiJSON('/chat/send', 'POST', { from_user_id: selfID, to_user_id: conv.peerID, text: tag });
  }

  fileInput.value = '';
  await loadMessagesForActive({ markRead: true });
}

// ===== events =====
sendBtn.addEventListener('click', () => {
  sendMessage().catch(err => setStatus('Ошибка отправки: ' + err.message, false));
});

msgInput.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    sendBtn.click();
  }
});

openDirectBtn.addEventListener('click', () => {
  openDirectChat().catch(err => setStatus('Ошибка диалога: ' + err.message, false));
});

createGroupBtn.addEventListener('click', () => {
  createGroup().catch(err => setStatus('Ошибка беседы: ' + err.message, false));
});

// ===== boot =====
ensureLogin().catch(err => {
  if (String(err && err.message) !== 'no session') setStatus('Ошибка: ' + err.message, false);
});