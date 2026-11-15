package declarative

import (
	"errors"
	"strings"
)

type incusDiskReference struct{ kind, pool, name, source string }

func incusBackupDevices(guest *incusArchiveGuest) (map[string]map[string]string, error) {
	if guest == nil {
		return nil, errors.New("missing Incus device metadata")
	}
	devices := guest.Devices
	if guest.ExpandedDevices != nil {
		devices = guest.ExpandedDevices
	} else if len(guest.Profiles) > 0 {
		return nil, errors.New("expanded profile devices are unavailable")
	}
	if len(devices) > 64 {
		return nil, errors.New("Incus backup device limit exceeded")
	}
	return devices, nil
}

// Shared by live discovery and private archive inspection. A volume subpath
// still requires the entire native volume, never a guessed host filesystem path.
func parseIncusBackupDisk(device map[string]string) (incusDiskReference, error) {
	invalid := errors.New("unsupported Incus backup disk reference")
	pool, source := device["pool"], device["source"]
	if device["type"] != "disk" || device["project"] != "" {
		return incusDiskReference{}, invalid
	}
	if device["path"] == "/" {
		if !bindingNamePattern.MatchString(pool) || source != "" {
			return incusDiskReference{}, invalid
		}
		return incusDiskReference{kind: "root", pool: pool}, nil
	}
	if pool == "" {
		return incusDiskReference{kind: "external", source: source}, nil
	}
	volume, subpath, hasSubpath := strings.Cut(source, "/")
	if !bindingNamePattern.MatchString(pool) || !bindingNamePattern.MatchString(volume) || hasSubpath && !portablePath(subpath) {
		return incusDiskReference{}, invalid
	}
	return incusDiskReference{kind: "volume", pool: pool, name: volume}, nil
}
