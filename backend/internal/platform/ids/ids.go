// Package ids creates unguessable, prefixed identifiers. Nothing in an identifier encodes a host,
// shard or time, so records can move between databases or regions without renumbering.
package ids

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// Prefixes keep identifiers self-describing in logs, URLs and support tickets.
const (
	Project      = "prj"
	Key          = "key"
	Run          = "run"
	Artifact     = "art"
	ToolCall     = "tc"
	Agent        = "agt"
	AgentVersion = "agv"
	Price        = "prc"
	Event        = "evt"
	Usage        = "use"
	Ledger       = "led"
	Lease        = "lse"
)

const randomBytes = 16

// New returns "<prefix>_<32 lowercase hex characters>" (128 random bits).
func New(prefix string) string {
	return prefix + "_" + Token(randomBytes)
}

// Token returns n random bytes as lowercase hex. It is used for lease tokens and secrets as well.
func Token(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic("cryptographic random source unavailable")
	}
	return hex.EncodeToString(buf)
}

// Valid reports whether value is a well-formed identifier with the given prefix.
func Valid(prefix, value string) bool {
	body, ok := strings.CutPrefix(value, prefix+"_")
	if !ok || len(body) != randomBytes*2 {
		return false
	}
	for _, c := range body {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
