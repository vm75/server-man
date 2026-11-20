package installation

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallFirecrackerPairStagesBoth(t *testing.T) {
	dir:=t.TempDir()
	oldF:=[]byte("previous firecracker")
	oldJ:=[]byte("previous jailer")
	for name,data:=range map[string][]byte{"firecracker":oldF,"jailer":oldJ} {
		if err:=os.WriteFile(filepath.Join(dir,name),data,0755);err!=nil{t.Fatal(err)}
	}
	// A malformed pair must be rejected before either existing binary changes.
	if err:=installFirecrackerPair(dir,map[string][]byte{"firecracker":[]byte("new")});err==nil {t.Fatal("incomplete pair accepted")}
	for name,want:=range map[string][]byte{"firecracker":oldF,"jailer":oldJ} {
		data,err:=os.ReadFile(filepath.Join(dir,name))
		if err!=nil || !bytes.Equal(data,want) {t.Fatal("preflight modified installed pair",name,err)}
	}
	next:=map[string][]byte{"firecracker":[]byte("new firecracker"),"jailer":[]byte("new jailer")}
	if err:=installFirecrackerPair(dir,next);err!=nil{t.Fatal(err)}
	for name,want:=range next {
		data,err:=os.ReadFile(filepath.Join(dir,name))
		if err!=nil || !bytes.Equal(data,want) {t.Fatal("installed pair differs",name,err)}
	}
}

func TestInstallFirecrackerPairRejectsSymlink(t *testing.T) {
	dir:=t.TempDir()
	external:=filepath.Join(t.TempDir(),"must-preserve")
	if err:=os.WriteFile(external,[]byte("untouched"),0600);err!=nil{t.Fatal(err)}
	if err:=os.Symlink(external,filepath.Join(dir,"firecracker"));err!=nil{t.Fatal(err)}
	if err:=installFirecrackerPair(dir,map[string][]byte{"firecracker":[]byte("new"),"jailer":[]byte("new")});err==nil {
		t.Fatal("symlinked existing executable accepted")
	}
	data,err:=os.ReadFile(external)
	if err!=nil || string(data)!="untouched" {t.Fatal("symlink target modified",err)}
	if _,err:=os.Lstat(filepath.Join(dir,"jailer"));!os.IsNotExist(err) {
		t.Fatal("partial binary published despite unsafe peer",err)
	}
}
