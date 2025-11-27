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

	// Статика: / -> отдать файлы из папки static
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/", fs)

	addr := ":8080"
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}

