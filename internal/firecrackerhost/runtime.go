package firecrackerhost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func writeUnitFile(path string, data []byte) error {
	// systemd's unit path is fixed and root-owned, never controlled by JSON.
	if filepath.Dir(path)!="/etc/systemd/system" || !strings.HasPrefix(filepath.Base(path),"sm-firecracker-fc") || !strings.HasSuffix(path,".service") {
		return errors.New("invalid Firecracker systemd unit path")
	}
	return writeRootFile(path,data,0644)
}
func tag(id string) string { return "server-man-firecracker:"+id }
func tapAlias(tap string) (string,error) {
	data,err:=os.ReadFile(filepath.Join("/sys/class/net",tap,"ifalias"))
	if err!=nil{return "",err}
	if len(data)>128{return "",errors.New("invalid TAP identity marker")}
	return strings.TrimSpace(string(data)),nil
}
func prepareTAP(ctx context.Context,spec Spec) error {
	id:=Identity(spec.UID,spec.ResourceID)
	tap:=TapName(spec.UUID)
	alias,err:=tapAlias(tap)
	if err==nil && alias!=tag(id) {
		return errors.New("Firecracker TAP identity conflicts with another interface")
	}
	if err!=nil {
		if !os.IsNotExist(err) {return errors.New("Firecracker TAP cannot be inspected safely")}
		if _,err=runCommand(ctx,IPPath,"tuntap","add","dev",tap,"mode","tap","user",fmt.Sprint(spec.UID));err!=nil{return err}
		if _,err=runCommand(ctx,IPPath,"link","set","dev",tap,"alias",tag(id));err!=nil{return err}
	}
	if _,err=runCommand(ctx,IPPath,"link","set","dev",tap,"master",spec.Bridge);err!=nil{return err}
	if _,err=runCommand(ctx,IPPath,"link","set","dev",tap,"up");err!=nil{return err}
	return nil
}
func removeTAP(ctx context.Context,spec Spec) error {
	id:=Identity(spec.UID,spec.ResourceID)
	tap:=TapName(spec.UUID)
	alias,err:=tapAlias(tap)
	if os.IsNotExist(err) {return nil}
	if err!=nil{return errors.New("Firecracker TAP ownership cannot be verified")}
	if alias!=tag(id) {return errors.New("Firecracker TAP belongs to another manager")}
	_,err=runCommand(ctx,IPPath,"link","delete","dev",tap)
	return err
}
func bindJailSource(from,to string) error {
	if info,err:=os.Lstat(to);err==nil {
		if !info.Mode().IsRegular() {return errors.New("unsafe Firecracker jail target")}
		// Unmount is never attempted here: stale mounts are handled by Cleanup.
		return errors.New("previous Firecracker jail input is not clean")
	} else if !os.IsNotExist(err) {return err}
	target,err:=os.OpenFile(to,os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW,0600)
	if err!=nil{return errors.New("cannot stage Firecracker jail input")}
	target.Close()
	src,err:=os.Open(from)
	if err!=nil{return errors.New("Firecracker source became inaccessible")}
	defer src.Close()
	before,err:=src.Stat()
	if err!=nil{return err}
	if err=syscall.Mount(from,to,"",syscall.MS_BIND,"");err!=nil{return errors.New("failed to bind Firecracker source")}
	after,err:=os.Stat(to)
	if err!=nil || !os.SameFile(before,after) {
		_ = syscall.Unmount(to,0)
		return errors.New("Firecracker source identity changed during staging")
	}
	return nil
}
func prepareJail(spec Spec,id string) error {
	root:=JailRoot(id)
	if _,err:=os.Lstat(root);err==nil {
		// Owned previous staging is expected after an unclean host shutdown.
		if err=Cleanup(id);err!=nil{return err}
	} else if !os.IsNotExist(err) {return errors.New("jail location is inaccessible")}
	if err:=secureDirectory(filepath.Join(root,"disks"));err!=nil{return err}
	if err:=bindJailSource(spec.Kernel,filepath.Join(root,"kernel"));err!=nil{return err}
	for n,source:=range spec.Disks {
		name:="root"
		if n>0 {name=fmt.Sprintf("disk%d",n)}
		if err:=bindJailSource(source,filepath.Join(root,"disks",name));err!=nil{return err}
	}
	data,err:=spec.NativeJSON()
	if err!=nil{return err}
	if err=writeRootFile(filepath.Join(root,"vm.json"),data,0644);err!=nil{return err}
	return nil
}
// Prepare is used only by the installed, root-owned systemd ExecStartPre helper.
// The jailed Firecracker binary never receives arbitrary host paths.
func Prepare(id string) error {
	if os.Geteuid()!=0 || !validOpaque(id) {return errors.New("Firecracker runtime requires root and an owned identifier")}
	spec,err:=readSpec(id)
	if err!=nil{return err}
	if err=checkSources(spec);err!=nil{return err}
	if err=prepareJail(spec,id);err!=nil{return err}
	ctx,cancel:=context.WithTimeout(context.Background(),20*time.Second)
	defer cancel()
	if err=prepareTAP(ctx,spec);err!=nil{
		_ = Cleanup(id)
		return err
	}
	// Marker is read-only ownership evidence, not evidence that guest userspace
	// or its HTTP application successfully started.
	if err=writeRootFile(filepath.Join(Root(id),"started.hash"),[]byte(runtimeHash(spec)+"\n"),0600);err!=nil {
		_ = Cleanup(id)
		return err
	}
	return nil
}
func unmountExpected(target string) error {
	info,err:=os.Lstat(target)
	if os.IsNotExist(err){return nil}
	if err!=nil{return err}
	if !info.Mode().IsRegular() {return errors.New("unsafe Firecracker jail mount target")}
	err=syscall.Unmount(target,0)
	if err!=nil && !errors.Is(err,syscall.EINVAL) && !errors.Is(err,syscall.ENOENT) {
		return errors.New("Firecracker jailed storage remains mounted")
	}
	return nil
}
// Cleanup runs only for the own stopped VM. It never removes source kernel or
// raw disk data, and refuses foreign interfaces.
func Cleanup(id string) error {
	if os.Geteuid()!=0 || !validOpaque(id) {return errors.New("Firecracker cleanup requires root and an owned identifier")}
	spec,err:=readSpec(id)
	if err!=nil{return err}
	root:=JailRoot(id)
	info,err:=os.Lstat(root)
	if err!=nil && !os.IsNotExist(err){return err}
	if err==nil {
		stat,ok:=info.Sys().(*syscall.Stat_t)
		if !info.IsDir() || !ok || (stat.Uid!=0 && stat.Uid!=uint32(spec.UID)) {return errors.New("foreign Firecracker jail root")}
		if err=unmountExpected(filepath.Join(root,"kernel"));err!=nil{return err}
		for n:=range spec.Disks {
			name:="root"
			if n>0 {name=fmt.Sprintf("disk%d",n)}
			if err=unmountExpected(filepath.Join(root,"disks",name));err!=nil{return err}
		}
		// All mounts above are no longer active. Never traverse the source
		// backing files when deleting the jail staging directory.
		if err=os.RemoveAll(root);err!=nil{return errors.New("Firecracker jail cleanup failed")}
	}
	ctx,cancel:=context.WithTimeout(context.Background(),20*time.Second)
	defer cancel()
	if err=removeTAP(ctx,spec);err!=nil{return err}
	_ = os.Remove(filepath.Join(Root(id),"started.hash"))
	return nil
}
// RuntimeMain handles fixed systemd hooks with zero environment-sensitive
// arguments. It is not sudo-authorized and cannot serve arbitrary requests.
func RuntimeMain(args []string) error {
	if os.Geteuid()!=0 || len(args)!=2 || !validOpaque(args[1]) {
		return errors.New("Firecracker runtime requires root, operation and identity")
	}
	switch args[0] {
	case "prepare":return Prepare(args[1])
	case "cleanup":return Cleanup(args[1])
	default:return errors.New("unsupported Firecracker runtime operation")
	}
}
var _ = time.Second
