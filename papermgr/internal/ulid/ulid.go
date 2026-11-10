// Package ulid implements the subset of ULID needed by PaperManage.
package ulid

import (
	"crypto/rand"
	"encoding/binary"
	"strings"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// New returns a 26-character Crockford base32 ULID.
func New() string {
	var b [16]byte
	ts := uint64(time.Now().UnixMilli())
	b[0] = byte(ts >> 40)
	b[1] = byte(ts >> 32)
	b[2] = byte(ts >> 24)
	b[3] = byte(ts >> 16)
	b[4] = byte(ts >> 8)
	b[5] = byte(ts)
	_, _ = rand.Read(b[6:])
	return encode(b)
}

// NewAt returns a ULID with a specific timestamp (useful in tests).
func NewAt(t time.Time) string {
	var b [16]byte
	ts := uint64(t.UnixMilli())
	b[0] = byte(ts >> 40)
	b[1] = byte(ts >> 32)
	b[2] = byte(ts >> 24)
	b[3] = byte(ts >> 16)
	b[4] = byte(ts >> 8)
	b[5] = byte(ts)
	_, _ = rand.Read(b[6:])
	return encode(b)
}

func encode(b [16]byte) string {
	// 128 bits -> 26 chars, first char only 3 bits.
	var out strings.Builder
	out.Grow(26)
	hi := binary.BigEndian.Uint64(b[:8])
	lo := binary.BigEndian.Uint64(b[8:])
	// Emit 130 bits with two leading zero bits.
	var bits [130]byte
	for i := 0; i < 64; i++ {
		bits[2+i] = byte((hi >> (63 - uint(i))) & 1)
	}
	for i := 0; i < 64; i++ {
		bits[66+i] = byte((lo >> (63 - uint(i))) & 1)
	}
	for i := 0; i < 26; i++ {
		var v byte
		for j := 0; j < 5; j++ {
			v = v<<1 | bits[i*5+j]
		}
		out.WriteByte(alphabet[v])
	}
	return out.String()
}

// Valid reports whether s looks like a ULID.
func Valid(s string) bool {
	if len(s) != 26 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(alphabet, r) {
			return false
		}
	}
	return true
}
