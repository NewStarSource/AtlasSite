package atlas

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
)

func InitOIDC(source string) error {
	config, err := LoadConfig(".local/development.json")
	if err != nil {
		return err
	}
	secret, err := os.ReadFile(source)
	if err != nil || len(secret) != 43 {
		return errors.New("本地账户客户端密钥不可用")
	}
	config.OIDCSecretFile = ".local/oidc-client.secret"
	config.CSRFKeyFile = ".local/csrf.keys"
	for filename, content := range map[string][]byte{config.OIDCSecretFile: secret, config.CSRFKeyFile: make([]byte, 32)} {
		if filename == config.CSRFKeyFile {
			if _, err = rand.Read(content); err != nil {
				return err
			}
		}
		if existing, readErr := os.ReadFile(filename); readErr == nil {
			if filename == config.OIDCSecretFile && string(existing) != string(secret) {
				return errors.New("现有客户端密钥不匹配，不覆盖")
			}
			continue
		} else if !os.IsNotExist(readErr) {
			return readErr
		}
		file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(content)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(".local/development.json", content, 0600)
}
