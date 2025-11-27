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
  if (body) {
    opts.headers = { 'Content-Type': 'application/json' };
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(url, opts);
  const text = await resp.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch (_) {}
  if (!resp.ok) {
    throw new Error(text || resp.status);
  }
  return data;
}

async function ensureLogin() {
  if (selfID !== null) return;

  const username = localStorage.getItem('ss_username') || sessionStorage.getItem('ss_username');
  const password = sessionStorage.getItem('ss_password');

  if (!username || !password) {
    window.location.href = '/login.html';
    return;
  }

  selfUser = username;
  selfUserInput.value = username;

  const data = await apiJSON('/login', 'POST', { username, password });
  selfID = data.id;
  chatSubtitle.textContent = 'Авторизован как ' + username;
}

function keyUser(name) {
  return 'u:' + name;
}

function keyGroup(id) {
  return 'g:' + String(id);
}

// ===== Личные чаты в localStorage =====

function loadDirectFromStorage() {
  let raw = null;
  try {
    raw = localStorage.getItem('ss_peers') || '[]';
  } catch (_) {
    raw = '[]';
  }
  let arr = [];
  try { arr = JSON.parse(raw); } catch (_) { arr = []; }

  arr.forEach((name) => {
    const k = keyUser(name);
    if (!conversations[k]) {
      conversations[k] = {
        type: 'user',
        title: name,
        peerName: name,
        peerID: null,
        lastKnown: 0,
        lastRead: 0,
        unread: 0
      };
    }
  });
}

function saveDirectToStorage() {
  const names = [];
  for (const [k, conv] of Object.entries(conversations)) {
    if (conv.type === 'user') names.push(conv.peerName);
  }
  try {
    localStorage.setItem('ss_peers', JSON.stringify(names));
  } catch (_) {}
}

// ===== Группы из бэкенда =====

async function loadGroups() {
  await ensureLogin();
  const groups = await apiJSON('/groups/by_user?user_id=' + selfID, 'GET');
  if (!Array.isArray(groups)) return;
  for (const g of groups) {
    const k = keyGroup(g.id);
    if (!conversations[k]) {
      conversations[k] = {
        type: 'group',
        title: g.name,
        groupID: g.id,
        lastKnown: 0,
        lastRead: 0,
        unread: 0
      };
    }
  }
}

// ===== Рендер списка чатов =====

function renderChatList() {
  chatListEl.innerHTML = '';
  const entries = Object.entries(conversations).map(([k, conv]) => ({
    key: k,
    title: conv.title,
    type: conv.type,
  }));
  entries.sort((a, b) => a.title.localeCompare(b.title));

  for (const item of entries) {
    const conv = conversations[item.key];
    const div = document.createElement('div');
    div.className = 'chat-item' + (item.key === activeKey ? ' active' : '');
    const nameSpan = document.createElement('div');
    nameSpan.className = 'chat-item-name';
    nameSpan.textContent = conv.title;

    const rightWrap = document.createElement('div');
    rightWrap.style.display = 'flex';
    rightWrap.style.alignItems = 'center';

    const pill = document.createElement('div');
    pill.className = 'chat-item-pill';
    pill.textContent = conv.type === 'group' ? 'группа' : 'личный';
    rightWrap.appendChild(pill);

    if (conv.unread && conv.unread > 0) {
      const badge = document.createElement('div');
      badge.className = 'unread-badge';
      badge.textContent = conv.unread > 99 ? '99+' : String(conv.unread);
      rightWrap.appendChild(badge);
    }

    div.appendChild(nameSpan);
    div.appendChild(rightWrap);

    div.addEventListener('click', () => {
      setActiveConversation(item.key).catch(err =>
        setStatus('Ошибка открытия чата: ' + err.message, false)
      );
    });

    chatListEl.appendChild(div);
  }
}

// ===== Рендер сообщений =====

function renderMessages(msgs) {
  messagesEl.innerHTML = '';
  if (!Array.isArray(msgs)) return 0;

  let maxID = 0;
  for (const m of msgs) {
    if (typeof m.id === 'number' && m.id > maxID) {
      maxID = m.id;
    }
    const wrapper = document.createElement('div');
    const meta = document.createElement('div');
    const body = document.createElement('div');

    const isSelf = (m.from_user_id === selfID);
    wrapper.className = 'msg ' + (isSelf ? 'self' : 'peer');
    meta.className = 'msg-meta';
    const who = isSelf ? 'Ты' : 'Собеседник';
    meta.textContent = m.created_at ? who + ' · ' + m.created_at : who;
    body.textContent = m.text;

    wrapper.appendChild(meta);
    wrapper.appendChild(body);
    messagesEl.appendChild(wrapper);
  }
  messagesEl.scrollTop = messagesEl.scrollHeight;
  return maxID;
}

// ===== unread для НЕактивных чатов =====

function updateUnreadFromMessages(conv, msgs) {
  if (!Array.isArray(msgs) || msgs.length === 0) {
    conv.lastKnown = conv.lastKnown || 0;
    conv.unread = conv.unread || 0;
    return;
  }

  let maxID = conv.lastKnown || 0;
  let unread = 0;

  for (const m of msgs) {
    if (typeof m.id === 'number' && m.id > maxID) {
      maxID = m.id;
    }
    if (typeof m.id === 'number' &&
        m.id > (conv.lastRead || 0) &&
        m.from_user_id !== selfID) {
      unread++;
    }
  }

  // Первый раз видим историю — считаем её прочитанной
  if ((conv.lastKnown || 0) === 0 && (conv.lastRead || 0) === 0) {
    conv.lastKnown = maxID;
    conv.lastRead = maxID;
    conv.unread = 0;
    return;
  }

  conv.lastKnown = maxID;
  conv.unread = unread;
}

async function refreshUnreadForOthers() {
  await ensureLogin();
  const entries = Object.entries(conversations);

  for (const [key, conv] of entries) {
    if (key === activeKey) continue;
    try {
      let msgs;
      if (conv.type === 'group') {
        msgs = await apiJSON('/groups/messages?group_id=' + conv.groupID, 'GET');
      } else {
        if (!conv.peerID) {
          const data = await apiJSON('/public_key?username=' + encodeURIComponent(conv.peerName), 'GET');
          conv.peerID = data.id;
        }
        const url = '/chat/messages?user_a=' + selfID + '&user_b=' + conv.peerID;
        msgs = await apiJSON(url, 'GET');
      }
      updateUnreadFromMessages(conv, msgs);
    } catch (err) {
      // тихо игнорим ошибки по отдельным чатам
    }
  }
  renderChatList();
}

// ===== загрузка сообщений для активного чата =====

async function loadMessagesForActive() {
  if (!activeKey) return;
  const conv = conversations[activeKey];
  if (!conv) return;
  await ensureLogin();

  if (conv.type === 'group') {
    const msgs = await apiJSON('/groups/messages?group_id=' + conv.groupID, 'GET');
    const maxID = renderMessages(msgs);
    if (maxID > 0) {
      conv.lastKnown = maxID;
      conv.lastRead = maxID;
      conv.unread = 0;
    }
  } else if (conv.type === 'user') {
    if (!conv.peerID) {
      const data = await apiJSON('/public_key?username=' + encodeURIComponent(conv.peerName), 'GET');
      conv.peerID = data.id;
    }
    const url = '/chat/messages?user_a=' + selfID + '&user_b=' + conv.peerID;
    const msgs = await apiJSON(url, 'GET');
    const maxID = renderMessages(msgs);
    if (maxID > 0) {
      conv.lastKnown = maxID;
      conv.lastRead = maxID;
      conv.unread = 0;
    }
  }

  renderChatList();
}

// ===== polling =====

function startPolling() {
  if (pollTimer) clearInterval(pollTimer);
  pollTimer = setInterval(async () => {
    try {
      await loadMessagesForActive();
      await refreshUnreadForOthers();
    } catch (_) {
      // ignore
    }
  }, 3000);
}

// ===== выбор активного чата =====

async function setActiveConversation(key) {
  activeKey = key;
  renderChatList();
  const conv = conversations[key];
  if (!conv) return;

  await ensureLogin();

  if (conv.type === 'group') {
    chatTitle.textContent = 'Группа: ' + conv.title;
    chatSubtitle.textContent = 'Групповая беседа';
    idsInfo.textContent = 'Ты: ' + selfID + ' · group_id: ' + conv.groupID;
  } else {
    chatTitle.textContent = 'Диалог с ' + conv.title;
    chatSubtitle.textContent = 'Личный чат';
    idsInfo.textContent = 'Ты: ' + selfID + ' · peer: ' + conv.peerName;
  }

  msgInput.disabled = false;
  sendBtn.disabled = false;

  await loadMessagesForActive();
  startPolling();
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
  } else if (conv.type === 'user') {
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

// ===== создание/открытие чатов =====

async function openDirectChat() {
  const input = document.getElementById('directNameInput');
  const name = (input.value || '').trim();
  if (!name) return;

  const k = keyUser(name);
  if (!conversations[k]) {
    conversations[k] = {
      type: 'user',
      title: name,
      peerName: name,
      peerID: null,
      lastKnown: 0,
      lastRead: 0,
      unread: 0
    };
    saveDirectToStorage();
  }

  input.value = '';
  renderChatList();
  await setActiveConversation(k);
}

async function createGroup() {
  const input = document.getElementById('groupNameInput');
  const name = (input.value || '').trim();
  if (!name) return;

  await ensureLogin();
  const created = await apiJSON('/groups/create', 'POST', {
    name: name,
    owner_id: selfID,
    member_ids: []
  });

  input.value = '';
  const k = keyGroup(created.id);
  conversations[k] = {
    type: 'group',
    title: created.name,
    groupID: created.id,
    lastKnown: 0,
    lastRead: 0,
    unread: 0
  };
  renderChatList();
  await setActiveConversation(k);
}

// ===== инициализация =====

document.addEventListener('DOMContentLoaded', async () => {
  try {
    await ensureLogin();
    setStatus('Вход выполнен', true);
  } catch (err) {
    setStatus('Ошибка входа: ' + err.message, false);
    return;
  }

  loadDirectFromStorage();
  try {
    await loadGroups();
  } catch (err) {
    setStatus('Ошибка загрузки бесед: ' + err.message, false);
  }

  renderChatList();

  // начальный прогон: узнать lastKnown/lastRead и НЕ считать историю непрочитанной
  try {
    await refreshUnreadForOthers();
  } catch (_) {}
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
  createGroup().catch(err => setStatus('Ошибка создания беседы: ' + err.message, false));
});
