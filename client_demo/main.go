package main

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
)

type LoginResponse struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	PublicKey          string `json:"public_key_base64"`
	PasswordSalt       string `json:"password_salt_base64"`
	EncPrivateKey      string `json:"enc_private_key_base64"`
	EncPrivateKeyNonce string `json:"enc_private_key_nonce_base64"`
}

type PublicKeyResponse struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	PublicKey string `json:"public_key_base64"`
}

type SendMessageRequest struct {
	FromUserID       int64  `json:"from_user_id"`
	ToUserID         int64  `json:"to_user_id"`
	CiphertextBase64 string `json:"ciphertext_base64"`
	NonceBase64      string `json:"nonce_base64"`
}

type MessageDTO struct {
	ID               int64  `json:"id"`
	FromUserID       int64  `json:"from_user_id"`
	ToUserID         int64  `json:"to_user_id"`
	CiphertextBase64 string `json:"ciphertext_base64"`
	NonceBase64      string `json:"nonce_base64"`
}

// те же параметры Argon2, что на сервере
type CryptoConfig struct {
	ArgonTime    uint32
	ArgonMemory  uint32
	ArgonThreads uint8
	ArgonKeyLen  uint32
}

var defaultCryptoConfig = CryptoConfig{
	ArgonTime:    1,
	ArgonMemory:  64 * 1024,
	ArgonThreads: 4,
	ArgonKeyLen:  32,
}

func encodeBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

func deriveKeyFromPassword(password string, salt []byte, cfg CryptoConfig) []byte {
	return argon2.IDKey([]byte(password), salt, cfg.ArgonTime, cfg.ArgonMemory, cfg.ArgonThreads, cfg.ArgonKeyLen)
}

func aesGCMEncrypt(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, aesgcm.NonceSize())
	_, err = rand.Read(nonce)
	if err != nil {
		return nil, nil, err
	}
	ciphertext = aesgcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

func aesGCMDecrypt(key, ciphertext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aesgcm.Open(nil, nonce, ciphertext, nil)
}

func deriveSessionKeyFromX25519(privateKeyBytes, peerPublicKeyBytes []byte) ([]byte, error) {
	curve := ecdh.X25519()
	priv, err := curve.NewPrivateKey(privateKeyBytes)
	if err != nil {
		return nil, err
	}
	pub, err := curve.NewPublicKey(peerPublicKeyBytes)
	if err != nil {
		return nil, err
	}
	shared, err := priv.ECDH(pub)
	if err != nil {
		return nil, err
	}
	h := hkdf.New(sha256.New, shared, nil, []byte("chat-session"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(h, key); err != nil {
		return nil, err
	}
	return key, nil
}

func EncryptMessageE2E(senderPriv, receiverPub, plaintext []byte) (ciphertext, nonce []byte, err error) {
	sessionKey, err := deriveSessionKeyFromX25519(senderPriv, receiverPub)
	if err != nil {
		return nil, nil, err
	}
	return aesGCMEncrypt(sessionKey, plaintext)
}

func DecryptMessageE2E(receiverPriv, senderPub, ciphertext, nonce []byte) ([]byte, error) {
	sessionKey, err := deriveSessionKeyFromX25519(receiverPriv, senderPub)
	if err != nil {
		return nil, err
	}
	return aesGCMDecrypt(sessionKey, ciphertext, nonce)
}

// helper: POST JSON
func httpPostJSON(url string, body any, out any) (*http.Response, error) {
	data, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if out != nil {
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return resp, err
			}
		}
	}
	return resp, nil
}

func main() {
	baseURL := flag.String("base", "http://localhost:8080", "server base URL")
	userName := flag.String("user", "alice", "current username")
	userPass := flag.String("pass", "", "current user password (default alicepass/bobpass)")
	peerName := flag.String("peer", "bob", "peer username to chat with")
	flag.Parse()

	if *userPass == "" {
		if *userName == "alice" {
			*userPass = "alicepass"
		} else if *userName == "bob" {
			*userPass = "bobpass"
		} else {
			fmt.Println("password is required for non-demo users, use -pass")
			return
		}
	}

	fmt.Printf("Client started as %s, chatting with %s\n", *userName, *peerName)

	// 1. Попробуем зарегистрировать (если уже есть — ок)
	type regReq struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	_, _ = httpPostJSON(*baseURL+"/register", regReq{*userName, *userPass}, nil)

	// 2. Логинимся
	var loginResp LoginResponse
	resp, err := httpPostJSON(*baseURL+"/login", regReq{*userName, *userPass}, &loginResp)
	if err != nil {
		panic(err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		panic(fmt.Sprintf("login status %d: %s", resp.StatusCode, string(b)))
	}

	// 3. Расшифровываем приватный ключ
	salt, err := decodeBase64(loginResp.PasswordSalt)
	if err != nil {
		panic(err)
	}
	encPriv, err := decodeBase64(loginResp.EncPrivateKey)
	if err != nil {
		panic(err)
	}
	privNonce, err := decodeBase64(loginResp.EncPrivateKeyNonce)
	if err != nil {
		panic(err)
	}

	derivedKey := deriveKeyFromPassword(*userPass, salt, defaultCryptoConfig)
	userPriv, err := aesGCMDecrypt(derivedKey, encPriv, privNonce)
	if err != nil {
		panic(err)
	}

	// 4. Получаем публичный ключ и id собеседника
	reqURL := fmt.Sprintf("%s/public_key?username=%s", *baseURL, *peerName)
	httpResp, err := http.Get(reqURL)
	if err != nil {
		panic(err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(httpResp.Body)
		panic(fmt.Sprintf("get public key %s: %d %s", *peerName, httpResp.StatusCode, string(b)))
	}
	var peerPKR PublicKeyResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&peerPKR); err != nil {
		panic(err)
	}
	peerPub, err := decodeBase64(peerPKR.PublicKey)
	if err != nil {
		panic(err)
	}
	selfID := loginResp.ID
	peerID := peerPKR.ID

	fmt.Printf("Logged in as %s (id=%d). Peer %s (id=%d)\n", loginResp.Username, selfID, peerPKR.Username, peerID)
	fmt.Println("Type messages and press Enter to send. Type /quit to exit.")

	// 5. Горутина-поллер, которая каждые 2 секунды скачивает и расшифровывает новые сообщения
	lastSeenID := int64(0)

	go func() {
		for {
			msgsURL := fmt.Sprintf("%s/messages?user_a=%d&user_b=%d", *baseURL, selfID, peerID)
			resp, err := http.Get(msgsURL)
			if err != nil {
				fmt.Println("poll error:", err)
				time.Sleep(2 * time.Second)
				continue
			}
			var msgs []MessageDTO
			if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
				resp.Body.Close()
				fmt.Println("decode messages error:", err)
				time.Sleep(2 * time.Second)
				continue
			}
			resp.Body.Close()

			for _, m := range msgs {
				if m.ID <= lastSeenID {
					continue
				}
				// интересуют только входящие сообщения
				if m.ToUserID != selfID {
					continue
				}
				ctBytes, err := decodeBase64(m.CiphertextBase64)
				if err != nil {
					fmt.Println("cipher b64 error:", err)
					continue
				}
				nBytes, err := decodeBase64(m.NonceBase64)
				if err != nil {
					fmt.Println("nonce b64 error:", err)
					continue
				}
				// расшифровываем, предполагая, что отправитель — наш peer
				plain, err := DecryptMessageE2E(userPriv, peerPub, ctBytes, nBytes)
				if err != nil {
					fmt.Printf("[msg %d] decrypt error: %v\n", m.ID, err)
					continue
				}
				fmt.Printf("\n[%s] %s\n> ", *peerName, string(plain))
				lastSeenID = m.ID
			}

			time.Sleep(2 * time.Second)
		}
	}()

	// 6. Читаем ввод пользователя и шлём сообщения
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for scanner.Scan() {
		text := scanner.Text()
		if text == "/quit" {
			fmt.Println("Exiting")
			return
		}
		if text == "" {
			fmt.Print("> ")
			continue
		}

		ct, msgNonce, err := EncryptMessageE2E(userPriv, peerPub, []byte(text))
		if err != nil {
			fmt.Println("encrypt error:", err)
			fmt.Print("> ")
			continue
		}

		sendReq := SendMessageRequest{
			FromUserID:       selfID,
			ToUserID:         peerID,
			CiphertextBase64: encodeBase64(ct),
			NonceBase64:      encodeBase64(msgNonce),
		}
		resp, err := httpPostJSON(*baseURL+"/send_message", sendReq, nil)
		if err != nil {
			fmt.Println("send error:", err)
			fmt.Print("> ")
			continue
		}
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			fmt.Printf("send status %d: %s\n", resp.StatusCode, string(b))
		}
		fmt.Print("> ")
	}
	if err := scanner.Err(); err != nil {
		fmt.Println("scanner error:", err)
	}
}
