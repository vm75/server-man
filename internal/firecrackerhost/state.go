package firecrackerhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"server-man/internal/core"
)

func ipBinary() string {
	for _,path:=range []string{"/usr/sbin/ip","/usr/bin/ip"} {
		if info,err:=os.Stat(path);err==nil && info.Mode().IsRegular() && info.Mode().Perm()&0111!=0 {return path}
	}
	return "/usr/sbin/ip"
}
func accountIDs(account string) (int, int, error) {
	u, err := user.Lookup(account)
	if err != nil { return 0, 0, errors.New("Firecracker workload account is unavailable") }
	uid, e1 := strconv.Atoi(u.Uid)
	gid, e2 := strconv.Atoi(u.Gid)
	if e1 != nil || e2 != nil || uid < 1 || gid < 1 { return 0, 0, errors.New("invalid workload account identity") }
	return uid,gid,nil
}
func validOpaque(id string) bool { return opaquePattern.MatchString(id) }
func secureDirectory(path string) error {
	relative,err:=filepath.Rel(BasePath,path)
	if err!=nil || !filepath.IsAbs(path) || filepath.Clean(path)!=path ||
		relative==".." || strings.HasPrefix(relative,"../") {
		return errors.New("invalid Firecracker state directory")
	}
	// Never let MkdirAll traverse a user-controlled symlink before checking
	// its ownership: walk from an independently trusted /var/lib parent.
	parent,err:=os.Lstat("/var/lib")
	if err!=nil{return err}
	pstat,ok:=parent.Sys().(*syscall.Stat_t)
	if !ok || !parent.IsDir() || pstat.Uid!=0 || parent.Mode().Perm()&0022!=0 {
		return errors.New("unsafe Firecracker state parent")
	}
	walk:=[]string{BasePath}
	if relative!="." {
		for _,component:=range strings.Split(relative,string(filepath.Separator)) {
			walk=append(walk,filepath.Join(walk[len(walk)-1],component))
		}
	}
	for _,dir:=range walk {
		if err=os.Mkdir(dir,0700);err!=nil && !os.IsExist(err) {
			return errors.New("Firecracker state directory cannot be created")
		}
		info,e:=os.Lstat(dir)
		if e!=nil || !info.IsDir() || info.Mode()&os.ModeSymlink!=0 {
			return errors.New("unsafe Firecracker state directory")
		}
		stat,ok:=info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid!=0 || info.Mode().Perm()&0077!=0 {
			return errors.New("Firecracker state directory ownership is unsafe")
		}
	}
	return nil
}
func regularRootFile(path string) ([]byte,error) {
	// Check the opened file descriptor: checking Lstat and then calling
	// os.ReadFile would permit replacement with a symlink in between.
	f,err:=os.OpenFile(path,os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,0)
	if err!=nil{return nil,err}
	defer f.Close()
	info,err:=f.Stat()
	if err!=nil{return nil,err}
	stat,ok:=info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid!=0 || info.Mode().Perm()&0022!=0 || info.Size()>64<<10 {
		return nil,errors.New("unsafe Firecracker native file")
	}
	return io.ReadAll(io.LimitReader(f,(64<<10)+1))
}
func writeRootFile(path string, data []byte, mode os.FileMode) error {
	if len(data)>64<<10 {return errors.New("oversize Firecracker native file")}
	dir:=filepath.Dir(path)
	if dir == "/etc/systemd/system" {
		info,err:=os.Lstat(dir)
		if err!=nil{return err}
		stat,ok:=info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || stat.Uid!=0 || info.Mode().Perm()&0022!=0 {
			return errors.New("unsafe Firecracker systemd directory")
		}
	} else if err:=secureDirectory(dir);err!=nil {return err}
	temp:=path+".pending"
	file,err:=os.OpenFile(temp,os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW,0600)
	if os.IsExist(err) {
		previous,e:=regularRootFile(temp)
		if e!=nil || !bytes.Equal(previous,data) {
			return errors.New("interrupted native staging differs from the expected bytes; inspect before retry")
		}
		if e=os.Remove(temp);e!=nil{return e}
		file,err=os.OpenFile(temp,os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW,0600)
	}
	if err!=nil {return errors.New("stale or unsafe native publication staging file")}
	defer os.Remove(temp)
	if _,err=file.Write(data);err!=nil {file.Close();return err}
	if err=file.Sync();err!=nil {file.Close();return err}
	if err=file.Chmod(mode);err!=nil {file.Close();return err}
	if err=file.Close();err!=nil {return err}
	if err=os.Rename(temp,path);err!=nil{return err}
	parent,err:=os.Open(dir)
	if err!=nil{return err}
	defer parent.Close()
	return parent.Sync()
}
func readSpec(id string) (Spec,error) {
	if !validOpaque(id) {return Spec{},errors.New("invalid Firecracker native identity")}
	data,err:=regularRootFile(filepath.Join(Root(id),"spec.json"))
	if err!=nil{return Spec{},err}
	dec:=json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var spec Spec
	if err=dec.Decode(&spec);err!=nil{return Spec{},errors.New("invalid owned Firecracker specification")}
	if spec.Validate()!=nil || Identity(spec.UID,spec.ResourceID)!=id {return Spec{},errors.New("Firecracker native identity has changed")}
	return spec,nil
}
func saveSpec(spec Spec) error {
	id:=Identity(spec.UID,spec.ResourceID)
	if err:=spec.Validate();err!=nil{return err}
	raw,err:=json.MarshalIndent(spec,"","  ")
	if err!=nil{return err}
	return writeRootFile(filepath.Join(Root(id),"spec.json"),append(raw,'\n'),0600)
}
func sameRuntime(a,b Spec) bool {
	a.Enabled=false
	b.Enabled=false
	aa,_:=json.Marshal(a)
	bb,_:=json.Marshal(b)
	return bytes.Equal(aa,bb)
}
func sourceFile(path string, uid int) (os.FileInfo,error) {
	if !filepath.IsAbs(path) || filepath.Clean(path)!=path {return nil,errors.New("invalid Firecracker disk path")}
	resolved,err:=filepath.EvalSymlinks(path)
	if err!=nil || resolved!=path {return nil,errors.New("Firecracker source cannot be a symlink")}
	info,err:=os.Lstat(path)
	if err!=nil || !info.Mode().IsRegular() || info.Size()<=0 {return nil,errors.New("Firecracker kernel or disk is missing or invalid")}
	stat,ok:=info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(uid) {return nil,errors.New("Firecracker source must be owned by the selected workload account")}
	return info,nil
}

func checkDiskSharing(spec Spec) error {
	// Storage-ID validation alone misses hardlinks and aliases representing
	// the same raw inode. Check all existing root-owned Firecracker specs.
	dir:=filepath.Join(BasePath,"workloads")
	entries,err:=os.ReadDir(dir)
	if os.IsNotExist(err) {return nil}
	if err!=nil{return errors.New("Firecracker native inventory cannot be verified")}
	if len(entries)>128{return errors.New("Firecracker native inventory exceeds the configured resource limit")}
	used:=map[string]bool{}
	inodes:=map[[2]uint64]bool{}
	for _,path:=range spec.Disks {
		info,e:=os.Stat(path)
		if e!=nil{return errors.New("Firecracker writable source is missing")}
		stat,ok:=info.Sys().(*syscall.Stat_t)
		if !ok{return errors.New("Firecracker disk identity cannot be established")}
		used[path]=true
		inodes[[2]uint64{uint64(stat.Dev),stat.Ino}]=true
	}
	self:=Identity(spec.UID,spec.ResourceID)
	for _,entry:=range entries {
		if !entry.IsDir() || entry.Name()==self {continue}
		if !validOpaque(entry.Name()) {
			return errors.New("foreign Firecracker native directory needs review")
		}
		other,e:=readSpec(entry.Name())
		if e!=nil{return errors.New("another managed VM's native disk inventory is uncertain")}
		for _,path:=range other.Disks {
			if used[path] {return errors.New("Firecracker writable disk is already assigned to another VM")}
			info,e:=os.Stat(path)
			if os.IsNotExist(e) {continue}
			if e!=nil{return errors.New("another Firecracker disk identity cannot be verified")}
			stat,ok:=info.Sys().(*syscall.Stat_t)
			if !ok{return errors.New("another Firecracker disk identity is unavailable")}
			if inodes[[2]uint64{uint64(stat.Dev),stat.Ino}] {
				return errors.New("Firecracker writable disk inode is shared with another VM")
			}
		}
	}
	return nil
}

func checkSources(spec Spec) error {
	if err:=spec.Validate();err!=nil{return err}
	if _,err:=sourceFile(spec.Kernel,spec.UID);err!=nil{return err}
	for _,path:=range spec.Disks {
		if _,err:=sourceFile(path,spec.UID);err!=nil{return err}
	}
	if err:=checkDiskSharing(spec);err!=nil{return err}
	if _,err:=os.Stat(filepath.Join("/sys/class/net",spec.Bridge,"bridge"));err!=nil {
		return errors.New("configured Linux bridge is unavailable")
	}
	if _,err:=os.Stat("/dev/kvm");err!=nil {
		return errors.New("KVM is unavailable")
	}
	for _,path:=range []string{RuntimePath,VMMPath,JailerPath,Systemctl,ipBinary()} {
		info,err:=os.Lstat(path)
		if err!=nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111==0 {
			return errors.New("Firecracker host dependency is unavailable")
		}
		stat,ok:=info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid!=0 || info.Mode().Perm()&0022!=0 {return errors.New("unsafe Firecracker host executable")}
	}
	return nil
}
func runCommand(ctx context.Context, program string, args ...string) (string,error) {
	// The executable is a constant, arguments are validated identifiers only.
	timeoutCtx,cancel:=context.WithTimeout(ctx,15*time.Second)
	defer cancel()
	cmd:=exec.CommandContext(timeoutCtx,program,args...)
	cmd.Env=[]string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin","LC_ALL=C","LANG=C"}
	output:=core.NewTail(32 << 10)
	cmd.Stdout,cmd.Stderr=output,output
	if err:=cmd.Run();err!=nil {return "",fmt.Errorf("Firecracker host operation failed: %w",err)}
	text,truncated:=output.Snapshot()
	if truncated{return "",errors.New("native Firecracker command output exceeded bound")}
	return text,nil
}
func isRunning(ctx context.Context,id string) (bool,error) {
	out,err:=runCommand(ctx,Systemctl,"show",UnitName(id),"--property=ActiveState","--value","--no-pager")
	if err!=nil {return false,err}
	state:=strings.TrimSpace(out)
	switch state {
	case "active","activating","reloading": return true,nil
	case "inactive","failed":return false,nil
	case "deactivating":return true,nil
	default:return false,errors.New("Firecracker unit state cannot be verified")
	}
}
