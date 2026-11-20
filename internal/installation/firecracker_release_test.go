package installation

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type releaseMember struct {
	name string
	data []byte
	kind byte
}

func releaseFixture(t *testing.T, members []releaseMember) (string,string) {
	t.Helper()
	var buffer bytes.Buffer
	gz:=gzip.NewWriter(&buffer)
	tarWriter:=tar.NewWriter(gz)
	for _,member:=range members {
		kind:=member.kind
		if kind==0 {kind=tar.TypeReg}
		h:=&tar.Header{Name:member.name,Mode:0755,Typeflag:kind}
		if kind==tar.TypeReg {h.Size=int64(len(member.data))}
		if err:=tarWriter.WriteHeader(h);err!=nil{t.Fatal(err)}
		if kind==tar.TypeReg {
			if _,err:=tarWriter.Write(member.data);err!=nil{t.Fatal(err)}
		}
	}
	if err:=tarWriter.Close();err!=nil{t.Fatal(err)}
	if err:=gz.Close();err!=nil{t.Fatal(err)}
	archive:=filepath.Join(t.TempDir(),"firecracker.tgz")
	if err:=os.WriteFile(archive,buffer.Bytes(),0600);err!=nil{t.Fatal(err)}
	hash:=sha256.Sum256(buffer.Bytes())
	return archive,hex.EncodeToString(hash[:])
}

func TestReadFirecrackerVerifiedReleasePair(t *testing.T) {
	arch:=""
	switch runtime.GOARCH {
	case "amd64":arch="x86_64"
	case "arm64":arch="aarch64"
	default:t.Skip("Firecracker is supported on amd64 and arm64")
	}
	elf,err:=os.ReadFile("/usr/bin/true")
	if err!=nil{t.Skip("ELF fixture unavailable")}
	release:="v1.99.0"
	prefix:="release-"+release+"-"+arch+"/"
	firecracker:=releaseMember{prefix+"firecracker-"+release+"-"+arch,elf,tar.TypeReg}
	jailer:=releaseMember{prefix+"jailer-"+release+"-"+arch,elf,tar.TypeReg}
	path,digest:=releaseFixture(t,[]releaseMember{firecracker,jailer})
	parsed,version,err:=readFirecrackerRelease(path,digest)
	if err!=nil || version!=release || !bytes.Equal(parsed["firecracker"],elf) || !bytes.Equal(parsed["jailer"],elf) {
		t.Fatalf("matching release rejected: %s %v",version,err)
	}
	if _,_,err=readFirecrackerRelease(path,"0000000000000000000000000000000000000000000000000000000000000000");err==nil {
		t.Fatal("release accepted an untrusted or incorrect checksum")
	}
	if _,_,err=readFirecrackerRelease(path,"short");err==nil {t.Fatal("invalid digest accepted")}
	for _,tt:=range []struct{name string;members []releaseMember}{
		{"missing jailer",[]releaseMember{firecracker}},
		{"duplicate binary",[]releaseMember{firecracker,firecracker,jailer}},
		{"version mismatch",[]releaseMember{firecracker,{name:"release-v1.98.0-"+arch+"/jailer-v1.98.0-"+arch,data:elf,kind:tar.TypeReg}}},
		{"symlink member",[]releaseMember{firecracker,jailer,{name:prefix+"link",kind:tar.TypeSymlink}}},
		{"wrong binary arch",[]releaseMember{{name:"release-v1.99.0-wrong_arch/firecracker-v1.99.0-wrong_arch",data:elf,kind:tar.TypeReg},jailer}},
	} {
		t.Run(tt.name,func(t *testing.T) {
			archive,sha:=releaseFixture(t,tt.members)
			if _,_,err:=readFirecrackerRelease(archive,sha);err==nil {t.Fatal("unsafe or incomplete Firecracker release accepted")}
		})
	}
}
