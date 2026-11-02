package deepseekweb

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EnvKeyFile lets the operator keep the at-rest encryption key outside
// DATA_DIR (for example on a different volume than backups). The file holds
// the base64 of 32 random bytes and must not be readable by group/others.
// Key custody is an open decision tied to #141; see docs/PERFIL_DEEPSEEK.es.md.
const EnvKeyFile = "CAPIMUX_DEEPSEEK_WEB_KEY_FILE"

const (
	keySize     = 32
	nonceSize   = 12
	blobVersion = byte(1)
	aadPrefix   = "capimux/deepseek-web/v1/"
)

var errCorrupt = errors.New("credencial cifrada inválida o clave incorrecta")

type sealer struct{ aead cipher.AEAD }

func newSealer(key []byte) (*sealer, error) {
	if len(key) != keySize {
		return nil, errors.New("clave de cifrado de sesiones inválida")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sealer{aead: aead}, nil
}

// seal encrypts plaintext bound to sessionID (AAD): a ciphertext copied into
// another session does not decrypt there.
func (s *sealer) seal(sessionID string, plaintext []byte) (string, error) {
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generar nonce: %w", err)
	}
	out := append([]byte{blobVersion}, nonce...)
	out = s.aead.Seal(out, nonce, plaintext, []byte(aadPrefix+sessionID))
	return base64.StdEncoding.EncodeToString(out), nil
}

func (s *sealer) open(sessionID, blob string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil || len(raw) < 1+nonceSize+s.aead.Overhead() || raw[0] != blobVersion {
		return nil, errCorrupt
	}
	pt, err := s.aead.Open(nil, raw[1:1+nonceSize], raw[1+nonceSize:], []byte(aadPrefix+sessionID))
	if err != nil {
		return nil, errCorrupt
	}
	return pt, nil
}

// loadOrCreateKey reads the key from keyFile (external custody) or from
// dir/secret.key, creating the latter (0600, dir 0700) on first use.
func loadOrCreateKey(dir, keyFile string) ([]byte, error) {
	if keyFile != "" {
		if runtime.GOOS != "windows" {
			st, err := os.Stat(keyFile)
			if err != nil {
				return nil, fmt.Errorf("no se pudo leer el archivo indicado en %s", EnvKeyFile)
			}
			if st.Mode().Perm()&0o077 != 0 {
				return nil, fmt.Errorf("el archivo indicado en %s no debe ser legible por grupo u otros (usar chmod 600)", EnvKeyFile)
			}
		}
		return readKey(keyFile, EnvKeyFile)
	}
	if err := ensurePrivateDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "secret.key")
	if _, err := os.Stat(path); err == nil {
		tightenPrivateFile(path)
		return readKey(path, "secret.key")
	}
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) { // lost a creation race: use the winner's key
			return readKey(path, "secret.key")
		}
		return nil, errors.New("no se pudo crear la clave de cifrado de sesiones")
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n")
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(path)
		return nil, errors.New("no se pudo guardar la clave de cifrado de sesiones")
	}
	return key, nil
}

func readKey(path, what string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la clave de cifrado (%s)", what)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(key) != keySize {
		return nil, fmt.Errorf("clave de cifrado inválida (%s): se esperan 32 bytes en base64", what)
	}
	return key, nil
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("no se pudo crear el directorio de sesiones")
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(dir); err == nil && st.Mode().Perm()&0o077 != 0 {
			_ = os.Chmod(dir, 0o700)
		}
	}
	return nil
}

func tightenPrivateFile(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	if st, err := os.Stat(path); err == nil && st.Mode().Perm()&0o077 != 0 {
		_ = os.Chmod(path, 0o600)
	}
}

func writePrivate(path string, key []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return errors.New("no se pudo escribir la clave nueva")
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n")
	if cerr := f.Close(); werr != nil || cerr != nil {
		_ = os.Remove(path)
		return errors.New("no se pudo escribir la clave nueva")
	}
	return nil
}
