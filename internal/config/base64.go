package config

import (
	"encoding/base64"
	"errors"
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func unb64(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("secrets: missing base64 field")
	}
	return base64.StdEncoding.DecodeString(s)
}
