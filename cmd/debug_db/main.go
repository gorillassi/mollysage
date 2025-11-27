package main

import (
    "database/sql"
    "fmt"
    _ "github.com/mattn/go-sqlite3"
)

func main() {
    db, err := sql.Open("sqlite3", "secure_chat.db")
    if err != nil {
        panic(err)
    }

    rows, err := db.Query(`SELECT id, from_user_id, to_user_id, hex(ciphertext), hex(nonce), created_at FROM messages`)
    if err != nil {
        panic(err)
    }
    defer rows.Close()

    for rows.Next() {
        var id, fromID, toID int64
        var ct, nonce, created string
        err := rows.Scan(&id, &fromID, &toID, &ct, &nonce, &created)
        if err != nil {
            panic(err)
        }
        fmt.Printf("[%d] %d -> %d | CT=%s | NONCE=%s | %s\n",
            id, fromID, toID, ct, nonce, created)
    }
}
