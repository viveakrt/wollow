package config

import (
	"fmt"
	"os"
)

type Config struct {
	Addr           string
	DataDir        string
	MasterKeyHex   string
	AdminPassword  string
	JWTSecret      string
}

func Load() (*Config, error) {
	cfg := &Config{
		Addr:          getEnv("WOLLOW_ADDR", ":8080"),
		DataDir:       getEnv("WOLLOW_DATA_DIR", "/data"),
		MasterKeyHex:  os.Getenv("WOLLOW_MASTER_KEY"),
		AdminPassword: os.Getenv("WOLLOW_ADMIN_PASSWORD"),
		JWTSecret:     os.Getenv("WOLLOW_JWT_SECRET"),
	}

	if cfg.MasterKeyHex == "" {
		return nil, fmt.Errorf("WOLLOW_MASTER_KEY is required (generate with: openssl rand -hex 32)")
	}
	if cfg.AdminPassword == "" {
		return nil, fmt.Errorf("WOLLOW_ADMIN_PASSWORD is required")
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("WOLLOW_JWT_SECRET is required (generate with: openssl rand -hex 32)")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
