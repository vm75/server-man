package installation

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type stagedReleaseBinary struct {
	name string
	file string
	previous []byte
	oldMode os.FileMode
	hadPrevious bool
}

// installFirecrackerPair stages both verified binaries before replacing either.
// If a rename fails, it tries to restore the old first binary. The operator
// must inspect and repair any failed rollback before starting a new VM.
func installFirecrackerPair(dir string, binaries map[string][]byte) error {
	names:=[]string{"firecracker","jailer"}
	staged:=make([]stagedReleaseBinary,0,2)
	defer func(){for _,s:=range staged{if s.file!=""{_ = os.Remove(s.file)}}}()
	for _,name:=range names {
		data,ok:=binaries[name]
		if !ok || len(data)==0 || len(data)>maxNativeBinaryBytes {
			return errors.New("verified Firecracker binary pair is missing or oversized")
		}
		previous:=stagedReleaseBinary{name:name}
		path:=filepath.Join(dir,name)
		old,err:=os.OpenFile(path,os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,0)
		if err!=nil && !os.IsNotExist(err){return fmt.Errorf("unsafe existing %s executable: %w",name,err)}
		if err==nil {
			info,e:=old.Stat()
			if e!=nil || !info.Mode().IsRegular() || info.Size()>maxNativeBinaryBytes || info.Mode().Perm()&0022!=0 {
				old.Close()
				return errors.New("existing Firecracker executable is unsafe; review before replacement")
			}
			previous.previous,e=io.ReadAll(io.LimitReader(old,maxNativeBinaryBytes+1))
			closeErr:=old.Close()
			if e!=nil || closeErr!=nil || len(previous.previous)>maxNativeBinaryBytes{return errors.New("existing Firecracker executable cannot be backed up")}
			previous.oldMode=info.Mode().Perm()
			previous.hadPrevious=true
		}
		temp,e:=os.CreateTemp(dir,".server-man-firecracker-*")
		if e!=nil{return e}
		previous.file=temp.Name()
		staged=append(staged,previous)
		if _,e=temp.Write(data);e!=nil{temp.Close();return e}
		if e=temp.Chmod(0755);e!=nil{temp.Close();return e}
		if e=temp.Sync();e!=nil{temp.Close();return e}
		if e=temp.Close();e!=nil{return e}
	}
	first:=staged[0]
	if err:=os.Rename(first.file,filepath.Join(dir,first.name));err!=nil{return err}
	staged[0].file=""
	second:=staged[1]
	if err:=os.Rename(second.file,filepath.Join(dir,second.name));err!=nil {
		var restore error
		if first.hadPrevious {
			restore=atomicFile(filepath.Join(dir,first.name),first.previous,first.oldMode)
		} else {
			restore=os.Remove(filepath.Join(dir,first.name))
		}
		if restore!=nil {
			return errors.New("Firecracker pair update failed and rollback failed; manual repair required before starting VMs")
		}
		return errors.New("Firecracker pair update failed; previous binary restored")
	}
	staged[1].file=""
	parent,e:=os.Open(dir)
	if e!=nil{return e}
	defer parent.Close()
	return parent.Sync()
}
