package declarative

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// The manifest is a private sibling of the per-resource generated directory,
// not a file inside it. Quadlet generators and application status inspect only
// native definitions in that directory; the sidecar must never become a unit.
const artifactManifestSuffix = ".server-man-artifacts.json"

type artifactManifest struct {
	Owner   string            `json:"owner"`
	Version int               `json:"version"`
	Files   map[string]string `json:"files"`
}

func artifactManifestPath(dir string) string { return dir + artifactManifestSuffix }

// A manifest records hashes, not generated file contents, secret values or
// configuration paths. Never trust a manifest alone to authorize overwrites:
// publishArtifacts still checks exact previous/desired file bytes.
func artifactManifestFor(files map[string][]byte) artifactManifest {
	m := artifactManifest{Owner: "server-man", Version: 1, Files: map[string]string{}}
	for name, data := range files {
		m.Files[name] = digest(data)
	}
	return m
}

func validArtifactManifest(m artifactManifest) bool {
	if m.Owner != "server-man" || m.Version != 1 || len(m.Files) > 256 || m.Files == nil {
		return false
	}
	for name, sum := range m.Files {
		if name == "" || name == "." || name == ".." || filepath.Base(name) != name || !revisionPattern.MatchString(sum) {
			return false
		}
	}
	return true
}

func readArtifactManifest(dir string) (artifactManifest, []byte, error) {
	path := artifactManifestPath(dir)
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return artifactManifest{}, nil, err
	}
	defer root.Close()
	f, err := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return artifactManifest{}, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return artifactManifest{}, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > MaxConfigBytes {
		return artifactManifest{}, nil, errors.New("unsafe publication manifest")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return artifactManifest{}, nil, err
	}
	if len(data) > MaxConfigBytes {
		return artifactManifest{}, nil, errors.New("publication manifest is oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var m artifactManifest
	if err := decoder.Decode(&m); err != nil {
		return artifactManifest{}, nil, errors.New("invalid publication manifest")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || !validArtifactManifest(m) {
		return artifactManifest{}, nil, errors.New("invalid publication manifest")
	}
	return m, data, nil
}

// writeArtifactManifest follows successful file publication and directory sync.
// A missing sidecar is an initial publication, not an alternate ownership path.
// Reviewed repair will require a recorded, matching manifest before it can
// replace bytes outside the current renderer's exact previous/desired sets.
func writeArtifactManifest(dir string, files map[string][]byte) error {
	_, prior, err := readArtifactManifest(dir)
	if err != nil && !os.IsNotExist(err) {
		return errArtifactConflict
	}
	hadPrior := err == nil
	m := artifactManifestFor(files)
	if !validArtifactManifest(m) {
		return errors.New("invalid publication artifact names")
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(files) != 0 && bytes.Equal(prior, data) {
		return nil
	}
	path := artifactManifestPath(dir)
	parent, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer parent.Close()
	name := filepath.Base(path)
	if len(files) == 0 {
		// A foreign process must not be able to replace the sidecar between
		// the initial ownership check and its deletion.
		_, latest, latestErr := readArtifactManifest(dir)
		if hadPrior {
			if latestErr != nil || !bytes.Equal(latest, prior) {
				return errArtifactConflict
			}
		} else if !os.IsNotExist(latestErr) {
			return errArtifactConflict
		}
		if err := parent.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else {
		temp := "." + name + ".new"
		f, err := parent.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer parent.Remove(temp)
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		// Refuse an outside-session edit to the prior sidecar before replacing it.
		_, latest, latestErr := readArtifactManifest(dir)
		if hadPrior {
			if latestErr != nil || !bytes.Equal(latest, prior) {
				return errArtifactConflict
			}
		} else if !os.IsNotExist(latestErr) {
			return errArtifactConflict
		}
		if err = parent.Rename(temp, name); err != nil {
			return err
		}
	}
	d, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
