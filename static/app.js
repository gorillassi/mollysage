let selfUser = null;
let selfID = null;

const conversations = {}; // key -> {type, title, peerName, peerID, groupID, lastKnown, lastRead, unread}
let activeKey = null;
let pollTimer = null;

// ===== DOM =====
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

const directNameInput = document.getElementById('directNameInput');
const groupNameInput = document.getElementById('groupNameInput');

const logoutBtn = document.getElementById('logoutBtn');
const addMemberBtn = document.getElementById('addMemberBtn');

// ===== utils =====
function setStatus(text, ok) {
  sideStatus.textContent = text || '';
  sideStatus.className = 'status';
  if (!text) return;
  if (ok) sideStatus.classList.add('ok');
  else sideStatus.classList.add('error');
}

function convKeyUser(peerName) { return 'u:' + peerName; }
function convKeyGroup(groupID) { return 'g:' + String(groupID); }

function avatarLetter(title) {
  const s = (title || '').trim();
  return s ? s[0].toUpperCase() : '?';
}

function nsKey(k) {
  // namespaced localStorage key per username
  return `ss:${selfUser || 'anon'}:${k}`;
}

function hardResetState() {
  // чистим in-memory
  for (const k of Object.keys(conversations)) delete conversations[k];
  activeKey = null;

  stopPolling();

  // чистим UI
  chatListEl.innerHTML = '';
  messagesEl.innerHTML = '';
  chatTitle.textContent = 'Чат не выбран';
  chatSubtitle.textContent = 'Выбери чат слева';
  idsInfo.textContent = '';
  msgInput.value = '';
  msgInput.disabled = true;
  sendBtn.disabled = true;
  if (attachBtn) attachBtn.disabled = true;
  if (addMemberBtn) addMemberBtn.disabled = true;
}

function logoutAndGo() {
  try {
    sessionStorage.removeItem('ss_username');
    sessionStorage.removeItem('ss_password');
  } catch (_) {}
  window.location.href = '/login.html';
}

// ===== api =====
async function apiJSON(url, method, body) {
  const opts = { method: method || 'GET' };
  if (body) {
    opts.headers = { 'Content-Type': 'application/json' };
    opts.body = JSON.stringify(body);
  }

  const resp = await fetch(url, opts);

  // важное: если сессия протухла/логин неверный — сразу выкидываем на login.html
  if (resp.status === 401) {
    logoutAndGo();
    return null;
  }

  if (!resp.ok) {
    const t = await resp.text().catch(() => '');
    throw new Error((resp.status ? (resp.status + ' ') : '') + (t || 'request failed'));
  }

  if (resp.status === 204) return null;
  const ct = resp.headers.get('content-type') || '';
  if (ct.includes('application/json')) return await resp.json();
  return await resp.text();
}

// ===== auth/session =====
async function ensureLogin() {
  if (selfID !== null) return;

  const username = sessionStorage.getItem('ss_username');
  const password = sessionStorage.getItem('ss_password');

  if (!username || !password) {
    window.location.href = '/login.html';
    return;
  }

  selfUser = username;

  // anti-bleed: если в прошлый раз был другой юзер — сбрасываем всё
  const lastUser = localStorage.getItem('ss:last_user');
  if (lastUser && lastUser !== username) {
    hardResetState();
  }
  localStorage.setItem('ss:last_user', username);

  selfUserInput.value = username;

  const login = await apiJSON('/login', 'POST', { username, password });
  selfID = login.id;

  setStatus(`Ок: ${username} (id=${selfID})`, true);

  // грузим чаты
  loadDirectFromStorage();
  await loadGroups();

  renderChatList();

  // старт: не считаем историю непрочитанной сразу
  try { await refreshUnreadBaseline(); } catch (_) {}
}

// ===== local direct chats =====
function loadDirectFromStorage() {
  let raw = '[]';
  try { raw = localStorage.getItem(nsKey('peers')) || '[]'; } catch (_) {}
  let arr = [];
  try { arr = JSON.parse(raw); } catch (_) { arr = []; }

  for (const name of arr) {
    const peerName = String(name || '').trim();
    if (!peerName) continue;
    const key = convKeyUser(peerName);
    if (!conversations[key]) {
      conversations[key] = {
        type: 'user',
        title: peerName,
        peerName,
        peerID: null,
        lastKnown: 0,
        lastRead: 0,
        unread: 0
      };
    }
  }
}

function saveDirectToStorage() {
  const names = [];
  for (const [k, conv] of Object.entries(conversations)) {
    if (conv.type === 'user') names.push(conv.peerName);
  }
  try { localStorage.setItem(nsKey('peers'), JSON.stringify(names)); } catch (_) {}
}

// ===== render =====
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
        lastRead: 0,
        unread: 0
      };
    } else {
      // обновим название, если поменяли
      conversations[key].title = g.name || conversations[key].title;
    }
  }
}

async function refreshUnreadBaseline() {
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

    let localMax = 0;
    for (const m of (msgs || [])) localMax = Math.max(localMax, m.id || 0);

    conv.lastKnown = localMax;
    conv.lastRead = localMax;
    conv.unread = 0;
  }
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

  conv.lastRead = conv.lastKnown;
  conv.unread = 0;

  renderChatList();
}

// ===== polling =====
function stopPolling() {
  if (pollTimer) clearInterval(pollTimer);
  pollTimer = null;
}

function startPolling() {
  stopPolling();
  pollTimer = setInterval(() => { pollOnce().catch(() => {}); }, 2000);
}

async function pollOnce() {
  if (!selfID) return;

  // группы могли добавиться/измениться — подтянем
  try { await loadGroups(); } catch (_) {}

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

    let localMax = 0;
    for (const m of (msgs || [])) localMax = Math.max(localMax, m.id || 0);
    conv.lastKnown = Math.max(conv.lastKnown || 0, localMax);

    if (key !== activeKey) {
      const base = conv.lastRead || 0;
      const diff = (conv.lastKnown || 0) - base;
      conv.unread = diff > 0 ? diff : 0;
    } else {
      conv.lastRead = conv.lastKnown;
      conv.unread = 0;
    }
  }

  renderChatList();
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
    addMemberBtn.disabled = false;
  } else {
    chatTitle.textContent = 'Диалог с ' + conv.title;
    chatSubtitle.textContent = 'Личный чат';
    idsInfo.textContent = 'Ты: ' + selfID + ' · peer: ' + conv.peerName;
    addMemberBtn.disabled = true;
  }

  msgInput.disabled = false;
  sendBtn.disabled = false;
  if (attachBtn) attachBtn.disabled = false;

  await loadMessagesForActive();
  startPolling();

  try { localStorage.setItem(nsKey('active'), key); } catch (_) {}
}

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
  await loadMessagesForActive();
}

async function openDirectChat() {
  await ensureLogin();

  const peerName = (directNameInput.value || '').trim();
  if (!peerName) throw new Error('Укажи логин собеседника');

  const key = convKeyUser(peerName);
  if (!conversations[key]) {
    conversations[key] = {
      type: 'user',
      title: peerName,
      peerName,
      peerID: null,
      lastKnown: 0,
      lastRead: 0,
      unread: 0
    };
    saveDirectToStorage();
  }

  renderChatList();
  await setActiveConversation(key);
}

async function createGroup() {
  await ensureLogin();

  const name = (groupNameInput.value || '').trim();
  if (!name) throw new Error('Укажи название беседы');

  const resp = await apiJSON('/groups/create', 'POST', {
    name,
    owner_id: selfID,
    member_ids: [] // владельца сервер обычно добавляет сам
  });

  const gid = resp.id;
  const key = convKeyGroup(gid);

  conversations[key] = {
    type: 'group',
    title: resp.name || name,
    groupID: gid,
    lastKnown: 0,
    lastRead: 0,
    unread: 0
  };

  renderChatList();
  groupNameInput.value = '';
  await setActiveConversation(key);
}

// ===== group: add member =====
async function addMemberToActiveGroup() {
  await ensureLogin();
  if (!activeKey) return;
  const conv = conversations[activeKey];
  if (!conv || conv.type !== 'group') return;

  const uname = prompt('Кого добавить? (username)', '');
  if (!uname) return;

  const peer = await apiJSON('/public_key?username=' + encodeURIComponent(uname.trim()), 'GET');
  const memberID = peer.id;

  // ожидаемый эндпоинт (если у тебя другое имя — скажи, поправлю):
  await apiJSON('/groups/add_member', 'POST', {
    group_id: conv.groupID,
    actor_id: selfID,
    member_id: memberID
  });

  setStatus(`Добавлен: ${uname} (id=${memberID})`, true);
}

// ===== attach photo (как было) =====
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
  if (resp.status === 401) {
    logoutAndGo();
    return;
  }
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
logoutBtn.addEventListener('click', () => logoutAndGo());

openDirectBtn.addEventListener('click', () => {
  openDirectChat().catch(err => setStatus(err.message, false));
});

createGroupBtn.addEventListener('click', () => {
  createGroup().catch(err => setStatus(err.message, false));
});

addMemberBtn.addEventListener('click', () => {
  addMemberToActiveGroup().catch(err => setStatus(err.message, false));
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

directNameInput.addEventListener('keydown', (e) => {
  if (e.key === 'Enter') openDirectBtn.click();
});

groupNameInput.addEventListener('keydown', (e) => {
  if (e.key === 'Enter') createGroupBtn.click();
});

// ===== boot =====
(async () => {
  await ensureLogin();

  // авто-открытие последнего чата для этого юзера
  try {
    const lastKey = localStorage.getItem(nsKey('active'));
    if (lastKey && conversations[lastKey]) {
      await setActiveConversation(lastKey);
    } else {
      renderChatList();
      startPolling();
    }
  } catch (_) {
    renderChatList();
    startPolling();
  }
})();