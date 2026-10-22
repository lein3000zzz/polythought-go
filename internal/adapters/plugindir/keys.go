package plugindir

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/jedisct1/go-minisign"
)

// TrustedKey is a minisign public key an admin trusts (D316), with the name versions it
// signed show as theirs.
type TrustedKey struct {
	Name string
	Key  minisign.PublicKey
}

// ParseTrustedKeys reads the keys an admin listed, one an entry: the key's line from its
// .pub file, after a name and a space when the admin gives it one, like
// "Polythought RWQf6LRC…". Without a name, a key goes by its ID.
func ParseTrustedKeys(entries []string) ([]TrustedKey, error) {
	out := make([]TrustedKey, 0, len(entries))

	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}

		name, line := "", e
		if i := strings.LastIndexByte(e, ' '); i > 0 {
			name, line = strings.TrimSpace(e[:i]), e[i+1:]
		}

		k, err := minisign.NewPublicKey(line)
		if err != nil || k.SignatureAlgorithm != [2]byte{'E', 'd'} {
			return nil, fmt.Errorf("%q is not a minisign public key: its line looks like RWQ… in the .pub file", e)
		}

		if name == "" {
			name = keyID(k.KeyId)
		}

		out = append(out, TrustedKey{Name: name, Key: k})
	}

	return out, nil
}

// keyID is a key's ID as minisign prints it: hex, as a little-endian number.
func keyID(id [8]byte) string {
	return fmt.Sprintf("%016X", binary.LittleEndian.Uint64(id[:]))
}
