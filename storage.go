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
