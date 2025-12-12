package main

import (
	"database/sql"
	"log"
	"net/http"

	_ "github.com/mattn/go-sqlite3"
)

func initDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	schema := `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,

    password_salt BLOB NOT NULL,
    password_hash BLOB NOT NULL,

    public_key BLOB NOT NULL,
    enc_private_key BLOB NOT NULL,
    enc_private_key_nonce BLOB NOT NULL,

    last_seen DATETIME
);

CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_user_id INTEGER NOT NULL,
    to_user_id   INTEGER NOT NULL,
    ciphertext   BLOB NOT NULL,
    nonce        BLOB NOT NULL,
    created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS plain_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_user_id INTEGER NOT NULL,
    to_user_id   INTEGER NOT NULL,
    text         TEXT NOT NULL,
    created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS plain_media (
  id INTEGER PRIMARY KEY AUTOINCREMENT,

  kind TEXT NOT NULL,              -- "direct" | "group"
  from_user_id INTEGER NOT NULL,
  to_user_id INTEGER,              -- для личных
  group_id INTEGER,                -- для групп

  ciphertext BLOB NOT NULL,
  nonce BLOB NOT NULL,

  content_type TEXT NOT NULL,
  original_name TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    owner_user_id INTEGER NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS group_members (
    group_id INTEGER NOT NULL,
    user_id  INTEGER NOT NULL,
    PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id INTEGER NOT NULL,
    from_user_id INTEGER NOT NULL,
    text TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
`
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}

	return db, nil
}

func main() {
	db, err := initDB("secure_chat.db")
	if err != nil {
		log.Fatalf("failed to init db: %v", err)
	}

	s := NewServer(db)

	// JSON API
	http.HandleFunc("/register", s.handleRegister)
	http.HandleFunc("/login", s.handleLogin)
	http.HandleFunc("/public_key", s.handleGetPublicKey)
	http.HandleFunc("/send_message", s.handleSendMessage)
	http.HandleFunc("/messages", s.handleGetMessages)

	http.HandleFunc("/chat/send", s.handleChatSend)
	http.HandleFunc("/chat/messages", s.handleChatMessages)
	http.HandleFunc("/chat/inbox", s.handleChatInbox)

	http.HandleFunc("/groups/create", s.handleCreateGroup)
	http.HandleFunc("/groups/add_member", s.handleAddGroupMember)
	http.HandleFunc("/groups/send", s.handleGroupSend)
	http.HandleFunc("/groups/messages", s.handleGroupMessages)
	http.HandleFunc("/groups/by_user", s.handleGroupsByUser)

	http.HandleFunc("/api/plain_media/upload", s.handlePlainMediaUpload)
	http.HandleFunc("/api/plain_media/get", s.handlePlainMediaGet)

	// Presence (БЕЗ srv)
	http.HandleFunc("/presence/ping", s.handlePresencePing)
	http.HandleFunc("/presence/online", s.handlePresenceOnline)

	// Статика
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/", fs)

	addr := ":8080"
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}