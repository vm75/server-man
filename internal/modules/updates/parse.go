package updates

import (
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

var packageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_.:-]{0,254}$`)
var aptInstall = regexp.MustCompile(`^Inst ([^ ]+)(?: \[([^\]]+)\])? \(([^ )]+)(?: .*)?\)(?: \[[^\]]*\])?$`)
var aptArch = regexp.MustCompile(`\[([^\]]+)\]\)(?: \[[^\]]*\])?$`)

func version(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}
func finalize(rows []Package) ([]Package, error) {
	if len(rows) > 2000 {
		return nil, errors.New("pending update list exceeds 2000 entries")
	}
	result := []Package{}
	seen := map[string]bool{}
	for _, p := range rows {
		if !packageName.MatchString(p.Name) || (p.Architecture != "" && !packageName.MatchString(p.Architecture)) || (p.Installed != "" && !version(p.Installed)) || (p.Candidate != "" && !version(p.Candidate)) || (p.Candidate == "" && p.Kind != "held") {
			return nil, errors.New("invalid package update data")
		}
		key := p.Name + "\x00" + p.Architecture + "\x00" + p.Candidate + "\x00" + p.Kind
		if !seen[key] {
			seen[key] = true
			result = append(result, p)
		}
	}
	slices.SortFunc(result, func(a, b Package) int {
		if a.Name != b.Name {
			return strings.Compare(a.Name, b.Name)
		}
		if a.Architecture != b.Architecture {
			return strings.Compare(a.Architecture, b.Architecture)
		}
		return strings.Compare(a.Candidate, b.Candidate)
	})
	return result, nil
}
func parseAPT(text string) ([]Package, error) {
	rows := []Package{}
	held := false
	recognized := false
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, " upgraded,") || strings.Contains(line, "Upgrading:") {
			recognized = true
		}
		if strings.Contains(line, "have been kept back:") || strings.Contains(line, "have been deferred due to phasing:") {
			held = true
			recognized = true
			continue
		}
		if held {
			if strings.HasPrefix(line, "  ") {
				for _, name := range strings.Fields(line) {
					rows = append(rows, Package{Name: name, Kind: "held"})
				}
				continue
			}
			held = false
		}
		if !strings.HasPrefix(line, "Inst ") {
			continue
		}
		recognized = true
		match := aptInstall.FindStringSubmatch(line)
		if match == nil {
			return nil, errors.New("unsupported APT simulation row")
		}
		p := Package{Name: match[1], Installed: match[2], Candidate: match[3], Kind: "upgrade"}
		if p.Installed == "" {
			p.Kind = "install"
		}
		if name, arch, ok := strings.Cut(p.Name, ":"); ok {
			p.Name, p.Architecture = name, arch
		} else if arch := aptArch.FindStringSubmatch(line); arch != nil {
			p.Architecture = arch[1]
		}
		rows = append(rows, p)
	}
	if !recognized {
		return nil, errors.New("unsupported APT simulation output")
	}
	return finalize(rows)
}
func parseDNF(text string) ([]Package, error) {
	rows := []Package{}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) != 3 {
			return nil, errors.New("unsupported DNF query row")
		}
		rows = append(rows, Package{Name: f[0], Architecture: f[1], Candidate: f[2], Kind: "upgrade"})
	}
	return finalize(rows)
}
func parsePacman(text string) ([]Package, error) {
	rows := []Package{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if (len(f) != 4 && len(f) != 5) || f[2] != "->" {
			return nil, errors.New("unsupported pacman query row")
		}
		p := Package{Name: f[0], Installed: f[1], Candidate: f[3], Kind: "upgrade"}
		if len(f) == 5 {
			if f[4] != "[ignored]" {
				return nil, errors.New("unsupported pacman suffix")
			}
			p.Kind = "held"
		}
		rows = append(rows, p)
	}
	return finalize(rows)
}
func parseZypper(text string) ([]Package, error) {
	decoder := xml.NewDecoder(strings.NewReader(text))
	rows := []Package{}
	stack := []string{}
	root, status, list := false, false, false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.Directive:
			return nil, errors.New("unsupported XML directive")
		case xml.StartElement:
			if len(stack) == 0 {
				if root || t.Name.Local != "stream" {
					return nil, errors.New("expected one zypper stream")
				}
				root = true
			}
			if len(stack) > 64 {
				return nil, errors.New("excessive XML nesting")
			}
			if t.Name.Local == "message" {
				for _, a := range t.Attr {
					if a.Name.Local == "type" && a.Value == "error" {
						return nil, errors.New("zypper reported a metadata error")
					}
				}
			}
			if t.Name.Local == "update-status" {
				status = true
			}
			if t.Name.Local == "update-list" {
				list = true
			}
			if t.Name.Local == "update" && len(stack) > 0 && stack[len(stack)-1] == "update-list" {
				var row struct {
					Name      string `xml:"name,attr"`
					Arch      string `xml:"arch,attr"`
					Candidate string `xml:"edition,attr"`
					Installed string `xml:"edition-old,attr"`
					Kind      string `xml:"kind,attr"`
				}
				if err := decoder.DecodeElement(&row, &t); err != nil {
					return nil, err
				}
				if row.Kind == "package" {
					rows = append(rows, Package{Name: row.Name, Architecture: row.Arch, Installed: row.Installed, Candidate: row.Candidate, Kind: "upgrade"})
				}
				continue
			}
			stack = append(stack, t.Name.Local)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errors.New("unbalanced XML")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 && strings.TrimSpace(string(t)) != "" {
				return nil, errors.New("text outside zypper XML")
			}
		}
	}
	if !root || !status || !list || len(stack) != 0 {
		return nil, errors.New("missing zypper update list")
	}
	return finalize(rows)
}
