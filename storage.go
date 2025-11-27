package main

import (
	"database/sql"
	"errors"
	"sync"
)

// ===== Пользователи — пока in-memory =====

type User struct {
	ID                 int64
	Username           string
	PasswordSalt       []byte
	PasswordHash       []byte
	PublicKey          []byte
	EncPrivateKey      []byte
	EncPrivateKeyNonce []byte
}

type UserStore struct {
	mu     sync.RWMutex
	byID   map[int64]*User
	byName map[string]*User
	nextID int64
}

func NewUserStore() *UserStore {
	return &UserStore{
		byID:   make(map[int64]*User),
		byName: make(map[string]*User),
		nextID: 1,
	}
}

var (
	ErrUserExists   = errors.New("user already exists")
	ErrUserNotFound = errors.New("user not found")
)

func (s *UserStore) CreateUser(u *User) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.byName[u.Username]; ok {
		return nil, ErrUserExists
	}

	u.ID = s.nextID
	s.nextID++

	s.byID[u.ID] = u
	s.byName[u.Username] = u

	return u, nil
}

func (s *UserStore) GetByUsername(username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	u, ok := s.byName[username]
	if !ok {
		return nil, ErrUserNotFound
	}
	return u, nil
}

func (s *UserStore) GetByID(id int64) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	u, ok := s.byID[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	return u, nil
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
		// в боевом коде логируем, тут просто пустой список
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

// ===== Groups (беседы) =====

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

// Все группы, где user_id состоит в group_members
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

// ===== Участники бесед =====

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

// ===== Сообщения в беседах =====

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
