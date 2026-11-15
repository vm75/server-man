package declarative

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"time"
)

var revisionPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (c Coordinator) checkApplyIdle() error {
	j, err := c.ReadJournal()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return errors.New("apply journal must be valid before backup operations")
	}
	if j.Stage != "complete" && j.Stage != "aborted" {
		return errors.New("an incomplete apply must be resolved before backup operations")
	}
	return nil
}

func (c Coordinator) ReadJournal() (Journal, error) {
	var j Journal
	if err := readPrivateJournal(c.Store.Path+".apply.json", &j); err != nil {
		return j, err
	}
	if err := validateJournal(j); err != nil {
		return j, err
	}
	return j, nil
}

func validateJournal(j Journal) error {
	if j.Version != 1 {
		return errors.New("invalid journal version")
	}
	if !revisionPattern.MatchString(j.DesiredRevision) || (j.BaseRevision != "missing" && !revisionPattern.MatchString(j.BaseRevision)) || len(j.Resources) > 192 {
		return errors.New("invalid journal revisions or resource limit")
	}
	if j.Mode != "now" && j.Mode != "next_start" {
		return errors.New("invalid journal apply mode")
	}
	switch j.Stage {
	case "prepared", "saved", "applying", "blocked", "failed", "complete", "aborted":
	default:
		return errors.New("invalid journal stage")
	}
	for key := range j.Resources {
		kind, id, ok := strings.Cut(key, "/")
		if !ok || (kind != "service" && kind != "share") || !validID(id) {
			return errors.New("invalid journal resource")
		}
	}
	if j.Superseded != nil {
		prior := j.Superseded
		if j.Stage != "prepared" || prior.Superseded != nil || prior.DesiredRevision != j.BaseRevision {
			return errors.New("invalid superseded journal")
		}
		switch prior.Stage {
		case "saved", "applying", "blocked", "failed":
		default:
			return errors.New("invalid superseded journal stage")
		}
		if err := validateJournal(*prior); err != nil {
			return err
		}
	}
	return nil
}

func (c Coordinator) writeJournal(j *Journal) error {
	j.UpdatedAt = time.Now().UTC()
	return writePrivateJournal(c.Store.Path+".apply.json", j)
}
