package services

import (
	"encoding/json"
	"errors"
	"strings"
)

// Modern systemd tables support JSON. Older versions may ignore the journal
// output flag for these commands, so accept their fixed uncolored columns too.
func parseList(text string, files bool) ([]Service, error) {
	result := []Service{}
	if strings.HasPrefix(strings.TrimSpace(text), "[") {
		var rows []struct {
			Unit        string `json:"unit"`
			UnitFile    string `json:"unit_file"`
			Load        string `json:"load"`
			Active      string `json:"active"`
			Sub         string `json:"sub"`
			Description string `json:"description"`
			State       string `json:"state"`
			Preset      string `json:"preset"`
		}
		if err := json.Unmarshal([]byte(text), &rows); err != nil {
			return nil, err
		}
		for _, r := range rows {
			item := Service{Name: r.Unit, Description: r.Description, Load: r.Load, Active: r.Active, Sub: r.Sub}
			if files {
				item.Name, item.Enabled, item.Preset = r.UnitFile, r.State, r.Preset
			}
			if !validUnit(item.Name) || (files && item.Enabled == "") || (!files && (item.Load == "" || item.Active == "" || item.Sub == "")) {
				return nil, errors.New("invalid service inventory row")
			}
			result = append(result, item)
		}
	} else {
		for _, line := range strings.Split(text, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if !validUnit(fields[0]) {
				return nil, errors.New("invalid service inventory unit")
			}
			item := Service{Name: fields[0]}
			if files {
				if len(fields) < 2 || len(fields) > 3 {
					return nil, errors.New("invalid unit-file columns")
				}
				item.Enabled = fields[1]
				if len(fields) == 3 {
					item.Preset = fields[2]
				}
			} else {
				if len(fields) < 4 {
					return nil, errors.New("invalid loaded-unit columns")
				}
				item.Load, item.Active, item.Sub = fields[1], fields[2], fields[3]
			}
			// Legacy loaded-unit output can add a dynamic JOB column. Details
			// provide an unambiguous description, so omit that trailing text here.
			result = append(result, item)
		}
	}
	if len(result) > 4096 {
		return nil, errors.New("service inventory exceeds 4096 units")
	}
	return result, nil
}
