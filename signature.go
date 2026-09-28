package wlturbo

import (
	"encoding/binary"
	"fmt"
)

// validateEventBody rejects truncated, unterminated and trailing wire values
// before generated code decodes or registers any children.
func validateEventBody(sig string, body []byte) error {
	pos := 0
	for len(sig) > 0 {
		end := 0
		for end < len(sig) && sig[end] != ',' {
			end++
		}
		kind := sig[:end]
		if end == len(sig) {
			return fmt.Errorf("invalid signature %q", sig)
		}
		sig = sig[end+1:]
		if kind == "fd" {
			continue
		}
		if len(body)-pos < 4 {
			return ErrMalformedFrame
		}
		switch kind {
		case "int", "uint", "fixed", "object", "new_id":
			pos += 4
		case "string", "array":
			n := uint64(binary.LittleEndian.Uint32(body[pos:]))
			pos += 4
			padded := (n + 3) &^ uint64(3)
			if padded > uint64(len(body)-pos) {
				return ErrMalformedFrame
			}
			if kind == "string" && n > 0 && body[pos+int(n)-1] != 0 {
				return ErrMalformedFrame
			}
			pos += int(padded)
		default:
			return fmt.Errorf("invalid argument kind %q: %w", kind, ErrMalformedFrame)
		}
	}
	if pos != len(body) {
		return ErrMalformedFrame
	}
	return nil
}
