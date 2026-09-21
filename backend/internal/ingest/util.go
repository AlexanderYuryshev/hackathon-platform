package ingest

import (
	"crypto/rand"
	"encoding/json"

	"github.com/hackaton-platform/backend/internal/scoring"
)

func readRandom(b []byte) (int, error) {
	return rand.Read(b)
}

func decodeNormalization(s string, n *scoring.Normalization) error {
	return json.Unmarshal([]byte(s), n)
}
