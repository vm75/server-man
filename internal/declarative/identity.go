package declarative

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"regexp"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// PrepareIdentities preserves existing guest identities when an editor omits
// them. New identities become part of the saved config, never a runtime cache.
func PrepareIdentities(candidate Config, previous Config) (Config, error) {
	// Parse a serialization to avoid mutating maps/pointers held by the caller.
	data, err := Encode(candidate)
	if err != nil {
		return Config{}, err
	}
	result, err := Parse(data)
	if err != nil {
		return Config{}, err
	}
	for id, s := range result.Services {
		if s.VM == nil {
			continue
		}
		if old, ok := previous.Services[id]; ok && old.VM != nil {
			if old.Backend != s.Backend {
				return Config{}, errors.New("changing a provisioned VM backend requires a new definition")
			}
			if (s.VM.UUID != "" && old.VM.UUID != "" && s.VM.UUID != old.VM.UUID) || (s.VM.MAC != "" && old.VM.MAC != "" && s.VM.MAC != old.VM.MAC) {
				return Config{}, errors.New("existing VM identity cannot be changed")
			}
			if s.VM.UUID == "" {
				s.VM.UUID = old.VM.UUID
			}
			if s.VM.MAC == "" {
				s.VM.MAC = old.VM.MAC
			}
		}
		if s.VM.UUID == "" {
			var b [16]byte
			if _, err = rand.Read(b[:]); err != nil {
				return Config{}, err
			}
			b[6] = (b[6] & 15) | 64
			b[8] = (b[8] & 63) | 128
			h := hex.EncodeToString(b[:])
			s.VM.UUID = h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
		}
		if s.VM.MAC == "" {
			var b [6]byte
			if _, err = rand.Read(b[:]); err != nil {
				return Config{}, err
			}
			b[0] = (b[0] | 2) & 254
			s.VM.MAC = net.HardwareAddr(b[:]).String()
		}
		result.Services[id] = s
	}
	return result, result.Validate()
}
