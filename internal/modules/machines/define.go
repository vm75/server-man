package machines

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"serverman/internal/core"
)

func (b *Backend) Define(ctx context.Context, file string, out io.Writer) (any, error) {
	if b.programs["virsh"] == "" {
		return nil, core.Unavailable("virsh is unavailable")
	}
	root := b.cfg.LibvirtXMLRoot
	if root == "" {
		root = b.cfg.VMRoot
	}
	if !filepath.IsAbs(file) {
		return nil, core.Invalid("XML file must be an absolute path inside libvirtXMLRoot")
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, core.Invalid("XML root is inaccessible")
	}
	realFile, err := filepath.EvalSymlinks(file)
	if err != nil {
		return nil, core.Invalid("XML file is inaccessible")
	}
	rel, err := filepath.Rel(realRoot, realFile)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, core.Invalid("XML file escapes libvirtXMLRoot")
	}
	f, err := os.OpenFile(realFile, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, core.Invalid("XML file is inaccessible")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, core.Invalid("XML must be a regular file no larger than 1 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, core.Invalid("XML could not be read within 1 MiB")
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, core.Invalid("invalid domain XML")
		}
		if _, directive := token.(xml.Directive); directive {
			return nil, core.Invalid("XML directives are unsupported")
		}
	}
	var domain struct {
		XMLName xml.Name `xml:"domain"`
		Type    string   `xml:"type,attr"`
		Name    string   `xml:"name"`
	}
	decoder = xml.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&domain) != nil || domain.XMLName.Space != "" || domain.Type != "kvm" || !validName("kvm", domain.Name) {
		return nil, core.Invalid("XML must define a KVM domain with a supported name")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, core.Invalid("XML must contain one domain")
	}
	// Pass the validated snapshot to libvirt, so a concurrent source edit cannot change it.
	snapshot, err := os.CreateTemp(b.cfg.StateDir, "domain-*.xml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(snapshot.Name())
	if _, err := snapshot.Write(data); err != nil {
		snapshot.Close()
		return nil, err
	}
	if err := snapshot.Close(); err != nil {
		return nil, err
	}
	c := b.command("kvm", "define", snapshot.Name(), "--validate")
	c.Timeout = time.Minute
	if err := b.runner.Run(ctx, c, out); err != nil {
		return nil, err
	}
	return map[string]string{"name": domain.Name, "action": "defined"}, nil
}
