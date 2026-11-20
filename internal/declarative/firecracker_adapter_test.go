package declarative

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"server-man/internal/core"
	"server-man/internal/firecrackerhost"
)

func TestFirecrackerAdapterPublicationAndLifecycleContracts(t *testing.T) {
	dir:=t.TempDir()
	store:=Store{Path:filepath.Join(dir,"server-man.yaml")}
	c,err:=Parse([]byte(firecrackerExample))
	if err!=nil{t.Fatal(err)}
	for path,data:=range map[string]string{
		"assets/vmlinux":"kernel",
		"data/root.ext4":"root",
		"data/extra.img":"extra",
	} {
		full:=filepath.Join(dir,path)
		if err=os.MkdirAll(filepath.Dir(full),0700);err!=nil{t.Fatal(err)}
		if err=os.WriteFile(full,[]byte(data),0600);err!=nil{t.Fatal(err)}
	}
	f:=NewFirecracker(store,nil)
	ops:=[]string{}
	active:=false
	registered:=false
	desiredEnabled:=false
	f.callHost=func(ctx context.Context,op string,input core.Input)(any,error){
		ops=append(ops,op)
		switch op {
		case "firecracker_validate":return map[string]any{"valid":true},nil
		case "firecracker_publish":
			registered=true
			native,ok:=input["spec"].(firecrackerhost.Spec)
			if !ok {t.Fatal("expected typed host input")}
			desiredEnabled=native.Enabled
			return firecrackerhost.Observation{Registered:true,Matches:true},nil
		case "firecracker_inspect":
			return firecrackerhost.Observation{Registered:registered,Matches:registered,Running:active,ActiveMatches:true,Enabled:desiredEnabled},nil
		case "firecracker_action":
			action:=input.String("action")
			if action=="start" || action=="restart" {active=true}
			if action=="stop" || action=="force_stop" {active=false}
			return firecrackerhost.Observation{Registered:true,Running:active},nil
		case "firecracker_remove":
			if active{return nil,errors.New("guest is running")}
			registered=false
			return firecrackerhost.Observation{},nil
		default:t.Fatalf("unexpected host operation %s",op)
		}
		return nil,nil
	}
	ctx:=context.Background()
	if err:=f.Validate(ctx,c,"service/web");err!=nil{t.Fatal(err)}
	if err:=f.Apply(ctx,Config{Version:1},c,"service/web","now");err!=nil{t.Fatal(err)}
	if active || !registered {t.Fatal("Apply started a guest or did not register it")}
	out,err:=f.Inspect(ctx,c,"service/web")
	if err!=nil || !out.Matches || out.Active {t.Fatal("registered observation",out,err)}
	if err=f.Action(ctx,c,"service/web","start");err!=nil{t.Fatal(err)}
	if !active{t.Fatal("Start did not activate")}
	// Boot enablement must not implicitly stop/restart a running guest.
	next:=c
	next.Services=map[string]Service{}
	for id,service:=range c.Services {
		service.Enabled=true
		next.Services[id]=service
	}
	if err=f.Apply(ctx,c,next,"service/web","next_start");err!=nil{t.Fatal(err)}
	if !active || !desiredEnabled {t.Fatal("boot policy altered execution state")}
	// Changing live VM hardware is rejected, even for next-start.
	changed:=next
	changed.Services=map[string]Service{}
	for id,service:=range next.Services {
		copyVM:=*service.VM
		copyVM.CPUs=4
		service.VM=&copyVM
		changed.Services[id]=service
	}
	if err=f.ValidateTransition(ctx,next,changed,"service/web");!errors.Is(err,errFirecrackerStoppedUpdate) {
		t.Fatal("running VM hardware edit not rejected",err)
	}
	if err=validateNativeTransition(ctx,f,next,changed,"service/web");!errors.Is(err,errFirecrackerStoppedUpdate) {
		t.Fatal("coordinator masked the actionable stop requirement",err)
	}
	if err=f.Apply(ctx,next,Config{Version:1},"service/web","now");err==nil {
		t.Fatal("active VM removal accepted")
	}
	if err=f.Action(ctx,next,"service/web","stop");err!=nil{t.Fatal(err)}
	if err=f.Apply(ctx,next,Config{Version:1},"service/web","now");err!=nil{t.Fatal(err)}
	for _,filename:=range []string{"assets/vmlinux","data/root.ext4","data/extra.img"} {
		if _,err=os.Stat(filepath.Join(dir,filename));err!=nil {t.Fatal("removal touched persistent VM data",err)}
	}
	if _,err=os.Stat(filepath.Join(f.resourceDir("service/web"),"instance.yaml"));!os.IsNotExist(err) {
		t.Fatal("generated artifact remained after removal",err)
	}
	if !strings.Contains(strings.Join(ops,","),"firecracker_remove") {
		t.Fatalf("missing backend removal call: %v",ops)
	}
}
