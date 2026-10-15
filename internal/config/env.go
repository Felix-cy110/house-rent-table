// Package config loads local application configuration without logging secrets.
package config

import (
	"bytes"
	"errors"
	"os"

	"github.com/joho/godotenv"
)

// LoadEnvFile is optional when the file is missing. Existing environment
// variables, including explicitly empty ones, always take precedence.
func LoadEnvFile(path string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("无法读取 .env，请检查文件权限")
	}
	// Windows editors can add a UTF-8 BOM before the first variable name.
	values, err := godotenv.Unmarshal(string(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})))
	if err != nil {
		// Parser errors can contain the original line and its credentials.
		return errors.New(".env 格式无效，请参考 .env.example 检查配置")
	}
	for key, value := range values {
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return errors.New("无法加载 .env，请检查配置项格式")
			}
		}
	}
	return nil
}
