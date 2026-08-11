package indexer

import (
	"strconv"
	"strings"
)

// EncodeKey returns an unambiguous encoding of ordered index components.
func EncodeKey(components []string) string {
	var encoded strings.Builder
	for _, component := range components {
		encoded.WriteString(strconv.Itoa(len(component)))
		encoded.WriteByte(':')
		encoded.WriteString(component)
	}
	return encoded.String()
}

// HasEncodedKeyPrefix matches complete leading components; an empty prefix never matches.
func HasEncodedKeyPrefix(encodedKey, encodedPrefix string) bool {
	if encodedPrefix == "" {
		return false
	}
	return strings.HasPrefix(encodedKey, encodedPrefix)
}
