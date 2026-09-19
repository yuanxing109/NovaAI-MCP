package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func EnsureToken(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	token, err := GenerateToken()
	if err != nil {
		return err
	}
	return WriteToken(path, token)
}

// WriteToken 原子写入 token 文件并收紧权限。
// token 文件是 token 的唯一事实来源：action.sh 展示它，服务端也读它。
func WriteToken(path, token string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(token), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return EnsurePerms(path)
}

func LoadToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func VerifyToken(presented, expected string) bool {
	if expected == "" {
		return false
	}
	return hmac.Equal([]byte(presented), []byte(expected))
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func ExtractBearer(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func EnsurePerms(path string) error {
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}
