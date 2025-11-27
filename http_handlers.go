package main

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type Server struct {
	users         *UserStore
	messages      *MessageStore
	plainMessages *PlainMessageStore
	crypto        CryptoConfig
}

func NewServer(db *sql.DB) *Server {
	return &Server{
		users:         NewUserStore(),
		messages:      NewMessageStore(db),
		plainMessages: NewPlainMessageStore(db),
		crypto:        defaultCryptoConfig,
	}
}


type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type RegisterResponse struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	PublicKey string `json:"public_key_base64"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		http.Error(w, "username and password required", http.StatusBadRequest)
		return
	}

	salt, err := generateRandomBytes(16)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	passwordKey := deriveKeyFromPassword(req.Password, salt, s.crypto)

	keyPair, err := generateUserKeyPair()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	encPriv, nonce, err := aesGCMEncrypt(passwordKey, keyPair.PrivateKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	user := &User{
		Username:           req.Username,
		PasswordSalt:       salt,
		PasswordHash:       passwordKey,
		PublicKey:          keyPair.PublicKey,
		EncPrivateKey:      encPriv,
		EncPrivateKeyNonce: nonce,
	}

	created, err := s.users.CreateUser(user)
	if err != nil {
		if errors.Is(err, ErrUserExists) {
			http.Error(w, "user already exists", http.StatusConflict)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := RegisterResponse{
		ID:        created.ID,
		Username:  created.Username,
		PublicKey: encodeBase64(created.PublicKey),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	PublicKey          string `json:"public_key_base64"`
	PasswordSalt       string `json:"password_salt_base64"`
	EncPrivateKey      string `json:"enc_private_key_base64"`
	EncPrivateKeyNonce string `json:"enc_private_key_nonce_base64"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		http.Error(w, "username and password required", http.StatusBadRequest)
		return
	}

	user, err := s.users.GetByUsername(req.Username)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	derived := deriveKeyFromPassword(req.Password, user.PasswordSalt, s.crypto)
	if !secureEqual(derived, user.PasswordHash) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	resp := LoginResponse{
		ID:                 user.ID,
		Username:           user.Username,
		PublicKey:          encodeBase64(user.PublicKey),
		PasswordSalt:       encodeBase64(user.PasswordSalt),
		EncPrivateKey:      encodeBase64(user.EncPrivateKey),
		EncPrivateKeyNonce: encodeBase64(user.EncPrivateKeyNonce),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func secureEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

type PublicKeyResponse struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	PublicKey string `json:"public_key_base64"`
}


func (s *Server) handleGetPublicKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	username := strings.TrimSpace(r.URL.Query().Get("username"))
	if username == "" {
		http.Error(w, "username required", http.StatusBadRequest)
		return
	}

	user, err := s.users.GetByUsername(username)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	resp := PublicKeyResponse{
		ID:       user.ID,
		Username: user.Username,
		PublicKey: encodeBase64(user.PublicKey),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type SendMessageRequest struct {
	FromUserID       int64  `json:"from_user_id"`
	ToUserID         int64  `json:"to_user_id"`
	CiphertextBase64 string `json:"ciphertext_base64"`
	NonceBase64      string `json:"nonce_base64"`
}

type SendMessageResponse struct {
	ID int64 `json:"id"`
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	if req.FromUserID == 0 || req.ToUserID == 0 || req.CiphertextBase64 == "" || req.NonceBase64 == "" {
		http.Error(w, "missing fields", http.StatusBadRequest)
		return
	}

	if _, err := s.users.GetByID(req.FromUserID); err != nil {
		http.Error(w, "from_user not found", http.StatusBadRequest)
		return
	}
	if _, err := s.users.GetByID(req.ToUserID); err != nil {
		http.Error(w, "to_user not found", http.StatusBadRequest)
		return
	}

	ct, err := decodeBase64(req.CiphertextBase64)
	if err != nil {
		http.Error(w, "bad ciphertext", http.StatusBadRequest)
		return
	}
	nonce, err := decodeBase64(req.NonceBase64)
	if err != nil {
		http.Error(w, "bad nonce", http.StatusBadRequest)
		return
	}

	msg := &Message{
		FromUserID: req.FromUserID,
		ToUserID:   req.ToUserID,
		Ciphertext: ct,
		Nonce:      nonce,
	}

	created, err := s.messages.CreateMessage(msg)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := SendMessageResponse{ID: created.ID}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type MessageDTO struct {
	ID               int64  `json:"id"`
	FromUserID       int64  `json:"from_user_id"`
	ToUserID         int64  `json:"to_user_id"`
	CiphertextBase64 string `json:"ciphertext_base64"`
	NonceBase64      string `json:"nonce_base64"`
}

func (s *Server) handleGetMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	userA := strings.TrimSpace(q.Get("user_a"))
	userB := strings.TrimSpace(q.Get("user_b"))
	if userA == "" || userB == "" {
		http.Error(w, "user_a and user_b required", http.StatusBadRequest)
		return
	}

	var idA, idB int64
	if _, err := fmt.Sscan(userA, &idA); err != nil || idA <= 0 {
		http.Error(w, "bad user_a", http.StatusBadRequest)
		return
	}
	if _, err := fmt.Sscan(userB, &idB); err != nil || idB <= 0 {
		http.Error(w, "bad user_b", http.StatusBadRequest)
		return
	}

	msgs := s.messages.ListBetween(idA, idB)
	out := make([]MessageDTO, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, MessageDTO{
			ID:               m.ID,
			FromUserID:       m.FromUserID,
			ToUserID:         m.ToUserID,
			CiphertextBase64: encodeBase64(m.Ciphertext),
			NonceBase64:      encodeBase64(m.Nonce),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type ChatSendRequest struct {
	FromUserID int64  `json:"from_user_id"`
	ToUserID   int64  `json:"to_user_id"`
	Text       string `json:"text"`
}

type ChatSendResponse struct {
	ID int64 `json:"id"`
}

func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChatSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	if req.FromUserID == 0 || req.ToUserID == 0 || strings.TrimSpace(req.Text) == "" {
		http.Error(w, "missing fields", http.StatusBadRequest)
		return
	}

	if _, err := s.users.GetByID(req.FromUserID); err != nil {
		http.Error(w, "from_user not found", http.StatusBadRequest)
		return
	}
	if _, err := s.users.GetByID(req.ToUserID); err != nil {
		http.Error(w, "to_user not found", http.StatusBadRequest)
		return
	}

	msg := &PlainMessage{
		FromUserID: req.FromUserID,
		ToUserID:   req.ToUserID,
		Text:       req.Text,
	}

	created, err := s.plainMessages.Create(msg)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := ChatSendResponse{ID: created.ID}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type ChatMessageDTO struct {
	ID         int64  `json:"id"`
	FromUserID int64  `json:"from_user_id"`
	ToUserID   int64  `json:"to_user_id"`
	Text       string `json:"text"`
	CreatedAt  string `json:"created_at"`
}

func (s *Server) handleChatMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	userA := strings.TrimSpace(q.Get("user_a"))
	userB := strings.TrimSpace(q.Get("user_b"))
	if userA == "" || userB == "" {
		http.Error(w, "user_a and user_b required", http.StatusBadRequest)
		return
	}

	var idA, idB int64
	if _, err := fmt.Sscan(userA, &idA); err != nil || idA <= 0 {
		http.Error(w, "bad user_a", http.StatusBadRequest)
		return
	}
	if _, err := fmt.Sscan(userB, &idB); err != nil || idB <= 0 {
		http.Error(w, "bad user_b", http.StatusBadRequest)
		return
	}

	msgs, err := s.plainMessages.ListBetween(idA, idB)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	out := make([]ChatMessageDTO, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, ChatMessageDTO{
			ID:         m.ID,
			FromUserID: m.FromUserID,
			ToUserID:   m.ToUserID,
			Text:       m.Text,
			CreatedAt:  m.CreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
