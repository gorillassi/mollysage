package main

import (
	"database/sql"
	"errors"
	"sync"
)

// ===== Пользователи — в SQLite (а не in-memory) =====

type User struct {
	ID                 int64
	Username           string
	PasswordSalt       []byte
	PasswordHash       []byte
	PublicKey          []byte
	EncPrivateKey      []byte
	EncPrivateKeyNonce []byte
	LastSeen           sql.NullString
}

type UserStore struct {
	mu sync.RWMutex
	db *sql.DB
}

func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{db: db}
}

var (
	ErrUserExists   = errors.New("user already exists")
	ErrUserNotFound = errors.New("user not found")
)

func (s *UserStore) CreateUser(u *User) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// проверим уникальность
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM users WHERE username = ? LIMIT 1`, u.Username).Scan(&exists)
	if err == nil {
		return nil, ErrUserExists
	}
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	res, err := s.db.Exec(`
		INSERT INTO users (username, password_salt, password_hash, public_key, enc_private_key, enc_private_key_nonce)
		VALUES (?,?,?,?,?,?)`,
		u.Username, u.PasswordSalt, u.PasswordHash, u.PublicKey, u.EncPrivateKey, u.EncPrivateKeyNonce,
	)
	if err != nil {
		// на всякий случай: если гонка — sqlite вернет constraint
		if sqliteIsConstraint(err) {
			return nil, ErrUserExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	u.ID = id
	return u, nil
}

func (s *UserStore) GetByUsername(username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var u User
	err := s.db.QueryRow(`
		SELECT id, username, password_salt, password_hash, public_key, enc_private_key, enc_private_key_nonce, last_seen
		FROM users WHERE username = ?`, username,
	).Scan(
		&u.ID, &u.Username, &u.PasswordSalt, &u.PasswordHash, &u.PublicKey, &u.EncPrivateKey, &u.EncPrivateKeyNonce, &u.LastSeen,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (s *UserStore) GetByID(id int64) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var u User
	err := s.db.QueryRow(`
		SELECT id, username, password_salt, password_hash, public_key, enc_private_key, enc_private_key_nonce, last_seen
		FROM users WHERE id = ?`, id,
	).Scan(
		&u.ID, &u.Username, &u.PasswordSalt, &u.PasswordHash, &u.PublicKey, &u.EncPrivateKey, &u.EncPrivateKeyNonce, &u.LastSeen,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (s *UserStore) UpdateLastSeen(userID int64) error {
	_, err := s.db.Exec(`UPDATE users SET last_seen = datetime('now') WHERE id = ?`, userID)
	return err
}

func (s *UserStore) ListOnline(seconds int) ([]User, error) {
	q := `
SELECT id, username, last_seen
FROM users
WHERE last_seen IS NOT NULL
  AND (strftime('%s','now') - strftime('%s', last_seen)) <= ?
ORDER BY username;
`
	rows, err := s.db.Query(q, seconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// очень простая проверка constraint для sqlite3
func sqliteIsConstraint(err error) bool {
	if err == nil {
		return false
	}
	// не тащим sqlite3.Error типом — просто по строке
	return containsAny(err.Error(), []string{"UNIQUE constraint failed", "constraint failed"})
}

func containsAny(s string, subs []string) bool {
	for _, x := range subs {
		if x != "" && (len(s) >= len(x)) && (indexOf(s, x) >= 0) {
			return true
		}
	}
	return false
}
func indexOf(s, sub string) int {
	// минимальный index, чтобы не тащить strings (если хочешь — замени на strings.Contains)
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ===== Сообщения — в SQLite =====

type Message struct {
	ID         int64
	FromUserID int64
	ToUserID   int64
	Ciphertext []byte
	Nonce      []byte
}

type MessageStore struct {
	db *sql.DB
}

func NewMessageStore(db *sql.DB) *MessageStore {
	return &MessageStore{db: db}
}

func (s *MessageStore) CreateMessage(m *Message) (*Message, error) {
	res, err := s.db.Exec(
		`INSERT INTO messages (from_user_id, to_user_id, ciphertext, nonce) VALUES (?, ?, ?, ?)`,
		m.FromUserID, m.ToUserID, m.Ciphertext, m.Nonce,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	m.ID = id
	return m, nil
}

func (s *MessageStore) ListBetween(userA, userB int64) []*Message {
	rows, err := s.db.Query(
		`SELECT id, from_user_id, to_user_id, ciphertext, nonce
         FROM messages
         WHERE (from_user_id = ? AND to_user_id = ?)
            OR (from_user_id = ? AND to_user_id = ?)
         ORDER BY id`,
		userA, userB, userB, userA,
	)
	if err != nil {
		return []*Message{}
	}
	defer rows.Close()

	var res []*Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.FromUserID, &m.ToUserID, &m.Ciphertext, &m.Nonce); err != nil {
			continue
		}
		res = append(res, &m)
	}
	return res
}

// ===== Plain messages for browser chat =====

type PlainMessage struct {
	ID         int64
	FromUserID int64
	ToUserID   int64
	Text       string
	CreatedAt  string
}

type PlainMessageStore struct {
	db *sql.DB
}

func NewPlainMessageStore(db *sql.DB) *PlainMessageStore {
	return &PlainMessageStore{db: db}
}

func (s *PlainMessageStore) Create(m *PlainMessage) (*PlainMessage, error) {
	res, err := s.db.Exec(
		`INSERT INTO plain_messages (from_user_id, to_user_id, text) VALUES (?, ?, ?)`,
		m.FromUserID, m.ToUserID, m.Text,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	m.ID = id
	return m, nil
}

func (s *PlainMessageStore) ListBetween(userA, userB int64) ([]*PlainMessage, error) {
	rows, err := s.db.Query(
		`SELECT id, from_user_id, to_user_id, text, created_at
         FROM plain_messages
         WHERE (from_user_id = ? AND to_user_id = ?)
            OR (from_user_id = ? AND to_user_id = ?)
         ORDER BY id`,
		userA, userB, userB, userA,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*PlainMessage
	for rows.Next() {
		var m PlainMessage
		if err := rows.Scan(&m.ID, &m.FromUserID, &m.ToUserID, &m.Text, &m.CreatedAt); err != nil {
			continue
		}
		res = append(res, &m)
	}
	return res, nil
}

// ===== Inbox (для автопоявления диалогов) =====

type InboxItem struct {
	PeerID        int64
	PeerUsername  string
	LastMessageID int64
	LastText      string
	LastCreatedAt string
}

func (s *PlainMessageStore) ListInbox(userID int64) ([]InboxItem, error) {
	const q = `
WITH last_per_peer AS (
  SELECT
    CASE WHEN from_user_id = ? THEN to_user_id ELSE from_user_id END AS peer_id,
    MAX(id) AS last_id
  FROM plain_messages
  WHERE from_user_id = ? OR to_user_id = ?
  GROUP BY peer_id
)
SELECT
  l.peer_id,
  u.username,
  pm.id,
  pm.text,
  pm.created_at
FROM last_per_peer l
JOIN users u ON u.id = l.peer_id
JOIN plain_messages pm ON pm.id = l.last_id
ORDER BY pm.id DESC;
`
	rows, err := s.db.Query(q, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []InboxItem{}
	for rows.Next() {
		var it InboxItem
		if err := rows.Scan(&it.PeerID, &it.PeerUsername, &it.LastMessageID, &it.LastText, &it.LastCreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ===== Groups / Media — оставил как у тебя (ниже без изменений по логике) =====

type Group struct {
	ID          int64
	Name        string
	OwnerUserID int64
	CreatedAt   string
}

type GroupStore struct {
	db *sql.DB
}

func NewGroupStore(db *sql.DB) *GroupStore {
	return &GroupStore{db: db}
}

func (s *GroupStore) Create(name string, ownerID int64) (*Group, error) {
	res, err := s.db.Exec(
		`INSERT INTO groups (name, owner_user_id) VALUES (?, ?)`,
		name, ownerID,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Group{
		ID:          id,
		Name:        name,
		OwnerUserID: ownerID,
	}, nil
}

func (s *GroupStore) GetByID(id int64) (*Group, error) {
	row := s.db.QueryRow(
		`SELECT id, name, owner_user_id, created_at FROM groups WHERE id = ?`,
		id,
	)
	var g Group
	if err := row.Scan(&g.ID, &g.Name, &g.OwnerUserID, &g.CreatedAt); err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *GroupStore) ListByUser(userID int64) ([]*Group, error) {
	rows, err := s.db.Query(
		`SELECT g.id, g.name, g.owner_user_id, g.created_at
         FROM groups g
         JOIN group_members gm ON gm.group_id = g.id
         WHERE gm.user_id = ?
         ORDER BY g.id`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.OwnerUserID, &g.CreatedAt); err != nil {
			continue
		}
		res = append(res, &g)
	}
	return res, nil
}

type GroupMemberStore struct {
	db *sql.DB
}

func NewGroupMemberStore(db *sql.DB) *GroupMemberStore {
	return &GroupMemberStore{db: db}
}

func (s *GroupMemberStore) AddMember(groupID, userID int64) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO group_members (group_id, user_id) VALUES (?, ?)`,
		groupID, userID,
	)
	return err
}

func (s *GroupMemberStore) IsMember(groupID, userID int64) (bool, error) {
	row := s.db.QueryRow(
		`SELECT 1 FROM group_members WHERE group_id = ? AND user_id = ?`,
		groupID, userID,
	)
	var dummy int
	err := row.Scan(&dummy)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

type GroupMessage struct {
	ID         int64
	GroupID    int64
	FromUserID int64
	Text       string
	CreatedAt  string
}

type GroupMessageStore struct {
	db *sql.DB
}

func NewGroupMessageStore(db *sql.DB) *GroupMessageStore {
	return &GroupMessageStore{db: db}
}

func (s *GroupMessageStore) Create(m *GroupMessage) (*GroupMessage, error) {
	res, err := s.db.Exec(
		`INSERT INTO group_messages (group_id, from_user_id, text) VALUES (?, ?, ?)`,
		m.GroupID, m.FromUserID, m.Text,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	m.ID = id
	return m, nil
}

func (s *GroupMessageStore) List(groupID int64) ([]*GroupMessage, error) {
	rows, err := s.db.Query(
		`SELECT id, group_id, from_user_id, text, created_at
         FROM group_messages
         WHERE group_id = ?
         ORDER BY id`,
		groupID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*GroupMessage
	for rows.Next() {
		var m GroupMessage
		if err := rows.Scan(&m.ID, &m.GroupID, &m.FromUserID, &m.Text, &m.CreatedAt); err != nil {
			continue
		}
		res = append(res, &m)
	}
	return res, nil
}

type PlainMedia struct {
	ID           int64
	Kind         string
	FromUserID   int64
	ToUserID     sql.NullInt64
	GroupID      sql.NullInt64
	Ciphertext   []byte
	Nonce        []byte
	ContentType  string
	OriginalName string
	CreatedAt    string
}

type PlainMediaStore struct{ db *sql.DB }

func NewPlainMediaStore(db *sql.DB) *PlainMediaStore { return &PlainMediaStore{db: db} }

func (s *PlainMediaStore) Create(m *PlainMedia) (*PlainMedia, error) {
	res, err := s.db.Exec(`
		INSERT INTO plain_media
		(kind, from_user_id, to_user_id, group_id, ciphertext, nonce, content_type, original_name)
		VALUES (?,?,?,?,?,?,?,?)`,
		m.Kind, m.FromUserID, m.ToUserID, m.GroupID,
		m.Ciphertext, m.Nonce, m.ContentType, m.OriginalName,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	m.ID = id
	return m, nil
}

func (s *PlainMediaStore) GetByID(id int64) (*PlainMedia, error) {
	var m PlainMedia
	err := s.db.QueryRow(`
		SELECT id, kind, from_user_id, to_user_id, group_id,
		       ciphertext, nonce, content_type, original_name, created_at
		FROM plain_media WHERE id=?`, id,
	).Scan(
		&m.ID, &m.Kind, &m.FromUserID, &m.ToUserID, &m.GroupID,
		&m.Ciphertext, &m.Nonce, &m.ContentType, &m.OriginalName, &m.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}