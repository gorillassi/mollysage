package main

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Server struct {
	users         *UserStore
	messages      *MessageStore
	plainMessages *PlainMessageStore
	groups        *GroupStore
	groupMembers  *GroupMemberStore
	groupMessages *GroupMessageStore
	crypto        CryptoConfig
	plainMedia    *PlainMediaStore
	plainMediaKey []byte
}


func NewServer(db *sql.DB) *Server {
	return &Server{
		users:         NewUserStore(db),
		messages:      NewMessageStore(db),
		plainMessages: NewPlainMessageStore(db),
		groups:        NewGroupStore(db),
		groupMembers:  NewGroupMemberStore(db),
		groupMessages: NewGroupMessageStore(db),
		crypto:        defaultCryptoConfig,
		plainMedia:    NewPlainMediaStore(db),
		plainMediaKey: mustLoadOrCreateServerKey("server_media_key.bin"),
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

// ====== Group chats (беседы) ======

type CreateGroupRequest struct {
	Name      string  `json:"name"`
	OwnerID   int64   `json:"owner_id"`
	MemberIDs []int64 `json:"member_ids"`
}

type CreateGroupResponse struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	OwnerID   int64  `json:"owner_id"`
	CreatedAt string `json:"created_at,omitempty"`
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.OwnerID == 0 {
		http.Error(w, "name and owner_id required", http.StatusBadRequest)
		return
	}

	// владелец должен существовать
	if _, err := s.users.GetByID(req.OwnerID); err != nil {
		http.Error(w, "owner not found", http.StatusBadRequest)
		return
	}

	g, err := s.groups.Create(req.Name, req.OwnerID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// добавляем владельца и остальных участников
	_ = s.groupMembers.AddMember(g.ID, req.OwnerID)
	for _, uid := range req.MemberIDs {
		if uid == 0 {
			continue
		}
		// опционально можно проверить, что юзер существует
		_ = s.groupMembers.AddMember(g.ID, uid)
	}

	resp := CreateGroupResponse{
		ID:      g.ID,
		Name:    g.Name,
		OwnerID: g.OwnerUserID,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type AddGroupMemberRequest struct {
	GroupID int64 `json:"group_id"`
	UserID  int64 `json:"user_id"`
}

func (s *Server) handleAddGroupMember(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AddGroupMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.GroupID == 0 || req.UserID == 0 {
		http.Error(w, "group_id and user_id required", http.StatusBadRequest)
		return
	}

	if _, err := s.groups.GetByID(req.GroupID); err != nil {
		http.Error(w, "group not found", http.StatusBadRequest)
		return
	}
	if _, err := s.users.GetByID(req.UserID); err != nil {
		http.Error(w, "user not found", http.StatusBadRequest)
		return
	}

	if err := s.groupMembers.AddMember(req.GroupID, req.UserID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type GroupSendRequest struct {
	GroupID    int64  `json:"group_id"`
	FromUserID int64  `json:"from_user_id"`
	Text       string `json:"text"`
}

type GroupSendResponse struct {
	ID int64 `json:"id"`
}

func (s *Server) handleGroupSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req GroupSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.GroupID == 0 || req.FromUserID == 0 || strings.TrimSpace(req.Text) == "" {
		http.Error(w, "missing fields", http.StatusBadRequest)
		return
	}

	if _, err := s.groups.GetByID(req.GroupID); err != nil {
		http.Error(w, "group not found", http.StatusBadRequest)
		return
	}

	if _, err := s.users.GetByID(req.FromUserID); err != nil {
		http.Error(w, "user not found", http.StatusBadRequest)
		return
	}

	isMember, err := s.groupMembers.IsMember(req.GroupID, req.FromUserID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !isMember {
		http.Error(w, "forbidden: not a group member", http.StatusForbidden)
		return
	}

	msg := &GroupMessage{
		GroupID:    req.GroupID,
		FromUserID: req.FromUserID,
		Text:       req.Text,
	}
	created, err := s.groupMessages.Create(msg)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := GroupSendResponse{ID: created.ID}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type GroupMessageDTO struct {
	ID           int64  `json:"id"`
	GroupID      int64  `json:"group_id"`
	FromUserID   int64  `json:"from_user_id"`
	FromUsername string `json:"from_username"`
	Text         string `json:"text"`
	CreatedAt    string `json:"created_at"`
}

func (s *Server) handleGroupMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gid := mustInt64(strings.TrimSpace(r.URL.Query().Get("group_id")))
	if gid <= 0 {
		http.Error(w, "group_id required", http.StatusBadRequest)
		return
	}

	// ВАЖНО: берём username отправителя через JOIN
	const q = `
SELECT gm.id, gm.group_id, gm.from_user_id, COALESCE(u.username, ''),
       gm.text, gm.created_at
FROM group_messages gm
LEFT JOIN users u ON u.id = gm.from_user_id
WHERE gm.group_id = ?
ORDER BY gm.id;
`
	rows, err := s.plainMedia.db.Query(q, gid) // если у тебя нет s.db, используй любой store с db
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := make([]GroupMessageDTO, 0, 64)
	for rows.Next() {
		var m GroupMessageDTO
		if err := rows.Scan(&m.ID, &m.GroupID, &m.FromUserID, &m.FromUsername, &m.Text, &m.CreatedAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type GroupDTO struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	OwnerID   int64  `json:"owner_id"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) handleGroupsByUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	uidStr := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if uidStr == "" {
		http.Error(w, "user_id required", http.StatusBadRequest)
		return
	}
	var uid int64
	if _, err := fmt.Sscan(uidStr, &uid); err != nil || uid <= 0 {
		http.Error(w, "bad user_id", http.StatusBadRequest)
		return
	}

	groups, err := s.groups.ListByUser(uid)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	out := make([]GroupDTO, 0, len(groups))
	for _, g := range groups {
		out = append(out, GroupDTO{
			ID:        g.ID,
			Name:      g.Name,
			OwnerID:   g.OwnerUserID,
			CreatedAt: g.CreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func mustInt64(s string) int64 {
	var v int64
	if _, err := fmt.Sscan(strings.TrimSpace(s), &v); err != nil || v <= 0 {
		return 0
	}
	return v
}

// POST multipart/form-data:
// kind=direct|group
// from_user_id=...
// to_user_id=... (для direct)
// group_id=... (для group)
// file=<image>
func (s *Server) handlePlainMediaUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(20 << 20); err != nil {
		http.Error(w, "bad multipart form", http.StatusBadRequest)
		return
	}

	kind := strings.TrimSpace(r.FormValue("kind"))
	fromID := mustInt64(r.FormValue("from_user_id"))
	if fromID == 0 {
		http.Error(w, "bad from_user_id", http.StatusBadRequest)
		return
	}

	// базовые проверки существования
	if _, err := s.users.GetByID(fromID); err != nil {
		http.Error(w, "from_user not found", http.StatusBadRequest)
		return
	}

	var toID int64
	var gid int64

	if kind == "direct" {
		toID = mustInt64(r.FormValue("to_user_id"))
		if toID == 0 {
			http.Error(w, "bad to_user_id", http.StatusBadRequest)
			return
		}
		if _, err := s.users.GetByID(toID); err != nil {
			http.Error(w, "to_user not found", http.StatusBadRequest)
			return
		}
	} else if kind == "group" {
		gid = mustInt64(r.FormValue("group_id"))
		if gid == 0 {
			http.Error(w, "bad group_id", http.StatusBadRequest)
			return
		}
		if _, err := s.groups.GetByID(gid); err != nil {
			http.Error(w, "group not found", http.StatusBadRequest)
			return
		}
		ok, err := s.groupMembers.IsMember(gid, fromID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	} else {
		http.Error(w, "bad kind", http.StatusBadRequest)
		return
	}

	f, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file required", http.StatusBadRequest)
		return
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, 20<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}

	contentType := hdr.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(raw)
	}
	if !strings.HasPrefix(contentType, "image/") {
		http.Error(w, "only images allowed", http.StatusBadRequest)
		return
	}

	// 🔐 ШИФРУЕМ ПЕРЕД ЗАПИСЬЮ В БД
	ct, nonce, err := aesGCMEncrypt(s.plainMediaKey, raw)
	if err != nil {
		http.Error(w, "crypto error", http.StatusInternalServerError)
		return
	}

	pm := &PlainMedia{
		Kind:         kind,
		FromUserID:   fromID,
		Ciphertext:   ct,
		Nonce:        nonce,
		ContentType:  contentType,
		OriginalName: hdr.Filename,
	}

	if kind == "direct" {
		pm.ToUserID = sql.NullInt64{Int64: toID, Valid: true}
	} else {
		pm.GroupID = sql.NullInt64{Int64: gid, Valid: true}
	}

	created, err := s.plainMedia.Create(pm)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int64{"id": created.ID})
}

func (s *Server) handlePlainMediaGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := mustInt64(r.URL.Query().Get("id"))
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}

	m, err := s.plainMedia.GetByID(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	raw, err := aesGCMDecrypt(s.plainMediaKey, m.Ciphertext, m.Nonce)
	if err != nil {
		http.Error(w, "decrypt failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", m.ContentType)
	w.Header().Set("Content-Disposition", `inline; filename="`+m.OriginalName+`"`)
	_, _ = w.Write(raw)
}

type InboxDTO struct {
	PeerID        int64  `json:"peer_id"`
	PeerUsername  string `json:"peer_username"`
	LastMessageID int64  `json:"last_message_id"`
	LastText      string `json:"last_text"`
	LastCreatedAt string `json:"last_created_at"`
}

func (s *Server) handleChatInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userStr := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if userStr == "" {
		http.Error(w, "user_id required", http.StatusBadRequest)
		return
	}
	var uid int64
	if _, err := fmt.Sscan(userStr, &uid); err != nil || uid <= 0 {
		http.Error(w, "bad user_id", http.StatusBadRequest)
		return
	}

	// (опционально) проверим что юзер существует
	if _, err := s.users.GetByID(uid); err != nil {
		http.Error(w, "user not found", http.StatusBadRequest)
		return
	}

	items, err := s.plainMessages.ListInbox(uid)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	out := make([]InboxDTO, 0, len(items))
	for _, it := range items {
		out = append(out, InboxDTO{
			PeerID:        it.PeerID,
			PeerUsername:  it.PeerUsername,
			LastMessageID: it.LastMessageID,
			LastText:      it.LastText,
			LastCreatedAt: it.LastCreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// пример: 15 секунд считаем online
const onlineWindowSec = 15

func (s *Server) handlePresencePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID <= 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	_ = s.users.UpdateLastSeen(req.UserID)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handlePresenceOnline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	users, err := s.users.ListOnline(onlineWindowSec)
	if err != nil {
		http.Error(w, "db", http.StatusInternalServerError)
		return
	}
	type dto struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	out := make([]dto, 0, len(users))
	for _, u := range users {
		out = append(out, dto{ID: u.ID, Username: u.Username})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}