package installation

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strings"
)

var firecrackerReleaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
var firecrackerDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

const (
	maxReleaseBytes = 128 << 20
	maxNativeBinaryBytes = 64 << 20
)

// readFirecrackerRelease never extracts arbitrary tar paths. The operator
// supplies a trusted SHA-256 separately from the local official release tarball.
// No network access, version pin or unchecked binary download is involved.
func readFirecrackerRelease(path,digest string)(map[string][]byte,string,error) {
	if !firecrackerDigest.MatchString(digest) {
		return nil,"",errors.New("a lowercase 64-character trusted SHA-256 checksum is required")
	}
	arch:=""
	machine:=elf.Machine(0)
	switch runtime.GOARCH {
	case "amd64":arch,machine="x86_64",elf.EM_X86_64
	case "arm64":arch,machine="aarch64",elf.EM_AARCH64
	default:return nil,"",errors.New("unsupported Firecracker host architecture")
	}
	file,err:=os.Open(path)
	if err!=nil{return nil,"",err}
	defer file.Close()
	info,err:=file.Stat()
	if err!=nil || !info.Mode().IsRegular() || info.Size()<1 || info.Size()>maxReleaseBytes {
		return nil,"",errors.New("Firecracker release must be a bounded regular file")
	}
	hash:=sha256.New()
	if _,err=io.Copy(hash,io.LimitReader(file,maxReleaseBytes+1));err!=nil{return nil,"",err}
	if hex.EncodeToString(hash.Sum(nil))!=digest {
		return nil,"",errors.New("Firecracker release SHA-256 differs from the supplied trusted digest")
	}
	if _,err=file.Seek(0,io.SeekStart);err!=nil{return nil,"",err}
	gzipReader,err:=gzip.NewReader(file)
	if err!=nil{return nil,"",errors.New("Firecracker release is not a gzip archive")}
	defer gzipReader.Close()
	reader:=tar.NewReader(gzipReader)
	found:=map[string][]byte{}
	version:=""
	var total int64
	for entries:=0;;entries++ {
		if entries>=256{return nil,"",errors.New("Firecracker release has too many members")}
		header,e:=reader.Next()
		if e==io.EOF{break}
		if e!=nil{return nil,"",errors.New("corrupt Firecracker release archive")}
		if header.Size<0 || header.Size>maxNativeBinaryBytes || total+header.Size>maxReleaseBytes {
			return nil,"",errors.New("oversized Firecracker release member")
		}
		total+=header.Size
		if header.Typeflag==tar.TypeDir{continue}
		if header.Typeflag!=tar.TypeReg && header.Typeflag!=tar.TypeRegA {
			return nil,"",errors.New("Firecracker release contains a non-regular entry")
		}
		parts:=strings.Split(header.Name,"/")
		if len(parts)!=2 {continue}
		prefix:="release-"
		if !strings.HasPrefix(parts[0],prefix) || !strings.HasSuffix(parts[0],"-"+arch) {continue}
		tag:=strings.TrimSuffix(strings.TrimPrefix(parts[0],prefix),"-"+arch)
		if !firecrackerReleaseTag.MatchString(tag) {continue}
		kind:=""
		switch parts[1] {
		case fmt.Sprintf("firecracker-%s-%s",tag,arch):kind="firecracker"
		case fmt.Sprintf("jailer-%s-%s",tag,arch):kind="jailer"
		default:continue
		}
		if version!="" && version!=tag {return nil,"",errors.New("mismatched Firecracker and jailer versions")}
		if found[kind]!=nil{return nil,"",errors.New("duplicate Firecracker release binary")}
		if header.Mode&0111==0 || header.Size==0 {return nil,"",errors.New("Firecracker release binary is not executable")}
		data:=make([]byte,int(header.Size))
		if _,err=io.ReadFull(reader,data);err!=nil{return nil,"",errors.New("truncated Firecracker release executable")}
		object,err:=elf.NewFile(bytes.NewReader(data))
		if err!=nil || object.Machine!=machine || object.Class!=elf.ELFCLASS64 {
			return nil,"",errors.New("Firecracker release executable is not a matching host ELF binary")
		}
		object.Close()
		found[kind]=data
		version=tag
	}
	if len(found)!=2{return nil,"",errors.New("release must contain one matching Firecracker binary and jailer")}
	return found,version,nil
}
