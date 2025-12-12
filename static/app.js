let selfUser = null;
let selfID = null;

const conversations = {}; // key -> {type, title, peerName, peerID, groupID, lastKnown, lastRead, unread}
let activeKey = null;
let pollTimer = null;

const chatListEl = document.getElementById('chatList');
const sideStatus = document.getElementById('sideStatus');
const selfUserInput = document.getElementById('selfUser');
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
const loginBtn = document.getElementById('loginBtn');

// (опционально) если добавишь кнопку в app.html — она появится
const addMemberBtn = document.getElementById('addMemberBtn');

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
  if (!resp.ok) {
    const t = await resp.text().catch(() => '');
    throw new Error(resp.status + ' ' + t);
  }
  if (resp.status === 204) return null;
  const ct = resp.headers.get('content-type') || '';
  if (ct.includes('application/json')) return await resp.json();
  return await resp.text();
}

// ===== localStorage per-user =====

function lsPrefix() {
  return selfUser ? `ss_${selfUser}_` : `ss_`;
}

function lsGet(k, defVal) {
  try {
    const v = localStorage.getItem(lsPrefix() + k);
    if (v === null || v === undefined) return defVal;
    return v;
  } catch (_) {
    return defVal;
  }
}

function lsSet(k, val) {
  try {
    localStorage.setItem(lsPrefix() + k, String(val));
  } catch (_) {}
}

function loadConvLastRead(key) {
  const v = Number(lsGet('lastRead_' + key, '0'));
  return Number.isFinite(v) ? v : 0;
}

function saveConvLastRead(key, id) {
  const v = Number(id || 0);
  lsSet('lastRead_' + key, String(v));
}

// ===== auth =====

async function ensureLogin() {
  if (selfID) return;

  const u = (selfUserInput.value || '').trim();
  if (!u) throw new Error('введи username');
  selfUser = u;

  // пароль в демке = username + "pass" (alicepass/bobpass)
  const pass = u + 'pass';

  // регистрируем (если уже есть — ок)
  try {
    await apiJSON('/register', 'POST', { username: u, password: pass });
  } catch (_) {}

  const login = await apiJSON('/login', 'POST', { username: u, password: pass });
  selfID = login.id;

  setStatus('Ок: ' + u + ' (id=' + selfID + ')', true);

  await loadGroups();

  // восстановим lastRead из localStorage для всех уже известных чатов
  for (const key of Object.keys(conversations)) {
    conversations[key].lastRead = loadConvLastRead(key);
  }

  // подтянуть inbox (чтобы диалоги появлялись даже если ты их не создавал вручную)
  try {
    await loadInbox();
  } catch (_) {}

  renderChatList();
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

function renderMessages(msgs) {
  messagesEl.innerHTML = '';
  if (!Array.isArray(msgs)) return 0;

  let maxID = 0;
  for (const m of msgs) {
    if (typeof m.id === 'number' && m.id > maxID) maxID = m.id;

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
  return maxID;
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
        lastKnown: 0,
        lastRead: loadConvLastRead(key),
        unread: 0
      };
    }
  }
}

async function loadInbox() {
  if (!selfID) return;
  const items = await apiJSON('/chat/inbox?user_id=' + encodeURIComponent(selfID), 'GET');
  if (!Array.isArray(items)) return;

  for (const it of items) {
    const peerName = String(it.peer_username || '').trim();
    if (!peerName) continue;

    const key = convKeyUser(peerName);
    if (!conversations[key]) {
      conversations[key] = {
        type: 'user',
        title: peerName,
        peerName: peerName,
        peerID: it.peer_id || null,
        lastKnown: 0,
        lastRead: loadConvLastRead(key),
        unread: 0
      };
    } else {
      conversations[key].peerID = conversations[key].peerID || it.peer_id || null;
    }
  }
}

// ===== polling + unread правильным способом =====

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
  }, 2000);
}

function calcUnread(conv, msgs) {
  const lastRead = Number(conv.lastRead || 0);
  let maxID = 0;
  let unread = 0;

  for (const m of (msgs || [])) {
    const id = Number(m.id || 0);
    if (id > maxID) maxID = id;
    if (id > lastRead && m.from_user_id !== selfID) unread++;
  }
  return { maxID, unread };
}

async function pollOnce() {
  if (!selfID) return;

  // подмешиваем новые диалоги из inbox (чтобы чат появлялся, даже если не открывали вручную)
  try { await loadInbox(); } catch (_) {}

  const keys = Object.keys(conversations);
  for (const key of keys) {
    const conv = conversations[key];

    let msgs = [];
    if (conv.type === 'group') {
      msgs = await apiJSON('/groups/messages?group_id=' + encodeURIComponent(conv.groupID), 'GET');
    } else {
      if (!conv.peerID) {
        const data = await apiJSON('/public_key?username=' + encodeURIComponent(conv.peerName), 'GET');
        conv.peerID = data.id;
      }
      msgs = await apiJSON('/chat/messages?user_a=' + encodeURIComponent(selfID) + '&user_b=' + encodeURIComponent(conv.peerID), 'GET');
    }

    const { maxID, unread } = calcUnread(conv, msgs);
    conv.lastKnown = Math.max(conv.lastKnown || 0, maxID);

    if (key !== activeKey) {
      conv.unread = unread;
    } else {
      // если чат открыт — считаем прочитанным всё что пришло
      conv.lastRead = conv.lastKnown;
      saveConvLastRead(key, conv.lastRead);
      conv.unread = 0;
    }
  }

  renderChatList();

  // если активный чат есть — обновим ленту
  if (activeKey) {
    await loadMessagesForActive();
  }
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
    idsInfo.textContent = 'Ты: ' + selfID + ' · group_id: ' + conv.groupID;
    if (addMemberBtn) addMemberBtn.style.display = '';
  } else {
    chatTitle.textContent = 'Диалог с ' + conv.title;
    chatSubtitle.textContent = 'Личный чат';
    idsInfo.textContent = 'Ты: ' + selfID + ' · peer: ' + conv.peerName;
    if (addMemberBtn) addMemberBtn.style.display = 'none';
  }

  msgInput.disabled = false;
  sendBtn.disabled = false;
  if (attachBtn) attachBtn.disabled = false;

  await loadMessagesForActive();
}

async function loadMessagesForActive() {
  if (!activeKey) return;
  const conv = conversations[activeKey];
  if (!conv) return;

  let msgs = [];
  if (conv.type === 'group') {
    msgs = await apiJSON('/groups/messages?group_id=' + encodeURIComponent(conv.groupID), 'GET');
  } else {
    if (!conv.peerID) {
      const data = await apiJSON('/public_key?username=' + encodeURIComponent(conv.peerName), 'GET');
      conv.peerID = data.id;
    }
    msgs = await apiJSON('/chat/messages?user_a=' + encodeURIComponent(selfID) + '&user_b=' + encodeURIComponent(conv.peerID), 'GET');
  }

  const maxID = renderMessages(msgs);
  conv.lastKnown = Math.max(conv.lastKnown || 0, maxID);

  // при открытом чате — прочитано
  conv.lastRead = conv.lastKnown;
  saveConvLastRead(activeKey, conv.lastRead);
  conv.unread = 0;

  renderChatList();
}

// ===== отправка сообщения =====

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
      text: text
    });
  } else {
    if (!conv.peerID) {
      const data = await apiJSON('/public_key?username=' + encodeURIComponent(conv.peerName), 'GET');
      conv.peerID = data.id;
    }
    await apiJSON('/chat/send', 'POST', {
      from_user_id: selfID,
      to_user_id: conv.peerID,
      text: text
    });
  }

  msgInput.value = '';
  await loadMessagesForActive();
}

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
      peerName: peerName,
      peerID: null,
      lastKnown: 0,
      lastRead: loadConvLastRead(key),
      unread: 0
    };
  }
  renderChatList();
  await setActiveConversation(key);
}

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
    lastKnown: 0,
    lastRead: loadConvLastRead(key),
    unread: 0
  };

  renderChatList();
  await setActiveConversation(key);
}

// ===== add member to group =====

async function addMemberToActiveGroup() {
  await ensureLogin();
  if (!activeKey) return;
  const conv = conversations[activeKey];
  if (!conv || conv.type !== 'group') return;

  const username = prompt('Кого добавить? (username):', 'bob');
  if (!username) return;
  const u = username.trim();
  if (!u) return;

  const pk = await apiJSON('/public_key?username=' + encodeURIComponent(u), 'GET');
  const uid = pk.id;

  await apiJSON('/groups/add_member', 'POST', { group_id: conv.groupID, user_id: uid });
  setStatus('Добавлен: ' + u + ' в "' + conv.title + '"', true);
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
  await loadMessagesForActive();
}

// ===== events =====

loginBtn.addEventListener('click', () => {
  ensureLogin().catch(err => setStatus('Ошибка логина: ' + err.message, false));
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

openDirectBtn.addEventListener('click', () => {
  openDirectChat().catch(err => setStatus('Ошибка диалога: ' + err.message, false));
});

createGroupBtn.addEventListener('click', () => {
  createGroup().catch(err => setStatus('Ошибка беседы: ' + err.message, false));
});

if (addMemberBtn) {
  addMemberBtn.addEventListener('click', () => {
    addMemberToActiveGroup().catch(err => setStatus('Ошибка добавления: ' + err.message, false));
  });
}