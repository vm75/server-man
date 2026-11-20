package firecrackerhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparedSourcePermissionsAndSymlinks(t *testing.T) {
	uid:=os.Getuid()
	if uid==0 { t.Skip("permissions test requires a non-root test identity") }
	dir:=t.TempDir()
	path:=filepath.Join(dir,"root.ext4")
	if err:=os.WriteFile(path,[]byte("raw disk fixture"),0600);err!=nil{t.Fatal(err)}
	if _,err:=sourceFile(path,uid,true);err!=nil{t.Fatal(err)}
	if err:=os.Chmod(path,0666);err!=nil{t.Fatal(err)}
	if _,err:=sourceFile(path,uid,true);err==nil{t.Fatal("world-writable source accepted")}
	if err:=os.Chmod(path,0400);err!=nil{t.Fatal(err)}
	if _,err:=sourceFile(path,uid,false);err!=nil{t.Fatal("read-only kernel should be accepted",err)}
	if _,err:=sourceFile(path,uid,true);err==nil{t.Fatal("read-only disk accepted")}
	if _,err:=sourceFile(path,uid+1,false);err==nil{t.Fatal("foreign-owner file accepted")}
	alias:=filepath.Join(dir,"alias")
	if err:=os.Symlink(path,alias);err!=nil{t.Fatal(err)}
	if _,err:=sourceFile(alias,uid,false);err==nil{t.Fatal("source alias symlink accepted")}
	missing:=filepath.Join(dir,"missing")
	if _,err:=sourceFile(missing,uid,false);err==nil{t.Fatal("missing source accepted")}
}

func TestFirecrackerNativeCommandOutputIsBounded(t *testing.T) {
	program:="/usr/bin/printf"
	if _,err:=os.Stat(program);err!=nil{t.Skip("printf fixture unavailable")}
	result,truncated,err:=runCommandBounded(context.Background(),program,4,"%s","0123456789")
	if err!=nil{t.Fatal(err)}
	if !truncated || !strings.HasSuffix(result,"6789"){t.Fatalf("unexpected bounded output %q, truncated=%v",result,truncated)}
	if _,err:=runCommand(context.Background(),program,"%s","okay");err!=nil{t.Fatal(err)}
}
