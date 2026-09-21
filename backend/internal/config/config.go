package config

import (
	"os"
	"strconv"
)

type Config struct {
	Addr         string
	DatabaseURL  string
	Env          string
	CookieSecure bool
}

func Load() Config {
	return Config{
		Addr:         getEnv("ADDR", ":8080"),
		DatabaseURL:  getEnv("DATABASE_URL", "postgres://hackathon:hackathon@localhost:5433/hackathon?sslmode=disable"),
		Env:          getEnv("APP_ENV", "dev"),
		CookieSecure: getEnvBool("COOKIE_SECURE", false),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
