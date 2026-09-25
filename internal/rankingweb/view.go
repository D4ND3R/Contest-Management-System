package rankingweb

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
)

// rowView is the data of one table row (language-neutral HTML, the same
// for every spectator, rendered once per change).
type rowView struct {
	B *ranking.Board
	R ranking.BoardRow
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
