//go:build firecracker_native

package declarative

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"server-man/internal/core"
	"server-man/internal/firecrackerhost"
)

// This is deliberately excluded from ordinary CI and go test ./.... It changes
// the host's systemd unit inventory, TAP devices, and a disposable guest disk.
// Run it as root only on a purpose-built, acknowledged KVM test machine with
// real Firecracker/jailer/runtime binaries installed at their documented paths.
func TestFirecrackerNativeAcceptance(t *testing.T) {
	if os.Getenv("SERVER_MAN_FIRECRACKER_NATIVE_ACK") != "disposable-kvm-only" {
		t.Skip("explicit disposable-KVM acceptance acknowledgement required")
	}
	if os.Geteuid() != 0 { t.Fatal("native acceptance must run as root on the disposable host") }
	account:=os.Getenv("SERVER_MAN_FIRECRACKER_ACCOUNT")
	kernel:=os.Getenv("SERVER_MAN_FIRECRACKER_KERNEL")
	root:=os.Getenv("SERVER_MAN_FIRECRACKER_ROOT")
	bridge:=os.Getenv("SERVER_MAN_FIRECRACKER_BRIDGE")
	httpURL:=os.Getenv("SERVER_MAN_FIRECRACKER_HTTP_URL")
	if account=="" || kernel=="" || root=="" || bridge=="" || httpURL=="" {
		t.Fatal("set ACCOUNT, KERNEL, ROOT, BRIDGE and HTTP_URL before native acceptance")
	}
	if !filepath.IsAbs(kernel) || !filepath.IsAbs(root) {
		t.Fatal("native acceptance kernel and root image paths must be absolute")
	}
	if httpURL[:min(len(httpURL),7)]!="http://" {
		t.Fatal("native acceptance requires a guest HTTP URL, not an HTTPS or arbitrary endpoint")
	}
	if _,err:=os.Stat(root);err!=nil {t.Fatal("prepared root image unavailable:",err)}
	if _,err:=os.Stat(kernel);err!=nil {t.Fatal("prepared guest kernel unavailable:",err)}
	var seed [16]byte
	if _,err:=rand.Read(seed[:]);err!=nil{t.Fatal(err)}
	seed[6]=(seed[6]&0x0f)|0x40
	seed[8]=(seed[8]&0x3f)|0x80
	id:="native"+hex.EncodeToString(seed[:4])
	uuid:=fmt.Sprintf("%x-%x-%x-%x-%x",seed[:4],seed[4:6],seed[6:8],seed[8:10],seed[10:16])
	mac:=fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x",seed[0],seed[1],seed[2],seed[3],seed[4])
	service:=Service{Kind:"vm",Backend:"firecracker",Enabled:false,
		VM:&VM{CPUs:1,MemoryMiB:256,Disks:[]string{"root"},Network:"network",UUID:uuid,MAC:mac,
			Firecracker:&FirecrackerVM{Kernel:"kernel"}}}
	c:=Config{
		Version:1,
		Services:map[string]Service{id:service},
		Bindings:map[string]Binding{"network":{Kind:"bridge",Name:bridge}},
		Storage:map[string]Storage{
			"kernel":{Kind:"file",External:true,Path:kernel},
			"root":{Kind:"file",External:true,Path:root,Format:"raw"},
		},
	}
	if err:=c.Validate();err!=nil{t.Fatal("invalid native fixture:",err)}
	store:=Store{Path:filepath.Join(t.TempDir(),"server-man.yaml")}
	backend:=NewFirecracker(store,nil)
	call:=func(ctx context.Context,operation string,in core.Input)(any,error){
		args,err:=json.Marshal(in)
		if err!=nil{return nil,err}
		return firecrackerhost.Handle(ctx,account,operation,args)
	}
	backend.callHost=call
	key:="service/"+id
	ctx,cancel:=context.WithTimeout(context.Background(),5*time.Minute)
	defer cancel()
	if err:=backend.Validate(ctx,c,key);err!=nil{t.Fatal("native prerequisites:",err)}
	published:=false
	t.Cleanup(func(){
		if !published{return}
		cleanupCtx,done:=context.WithTimeout(context.Background(),2*time.Minute)
		defer done()
		_ = backend.Action(cleanupCtx,c,key,"force_stop")
		if err:=backend.Apply(cleanupCtx,c,Config{Version:1},key,"now");err!=nil{
			t.Errorf("native test cleanup must be inspected manually for %s: %v",id,err)
		}
	})
	published=true
	if err:=backend.Apply(ctx,Config{Version:1},c,key,"now");err!=nil{t.Fatal("apply stopped definition:",err)}
	obs,err:=backend.readHost(ctx,c,key)
	if err!=nil || !obs.Registered || obs.Running || !obs.Matches {
		t.Fatalf("apply unexpectedly started guest or failed to publish: %+v %v",obs,err)
	}
	if err:=backend.Action(ctx,c,key,"start");err!=nil{t.Fatal("jailer guest startup:",err)}
	obs,err=backend.readHost(ctx,c,key)
	if err!=nil || !obs.Running || !obs.ActiveMatches {t.Fatalf("VMM startup was not verified: %+v %v",obs,err)}
	checkHTTP:=func(){
		client:=&http.Client{Timeout:2*time.Second}
		deadline:=time.Now().Add(45*time.Second)
		for {
			req,e:=http.NewRequestWithContext(ctx,http.MethodGet,httpURL,nil)
			if e!=nil{t.Fatal(e)}
			resp,e:=client.Do(req)
			if e==nil {
				resp.Body.Close()
				if resp.StatusCode>=200 && resp.StatusCode<500 {return}
			}
			if time.Now().After(deadline) {t.Fatal("guest did not respond to HTTP on the configured bridge")}
			select {
			case <-ctx.Done():t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
		}
	}
	checkHTTP()
	// A fresh adapter sees the same active guest; the VM is not tied to a
	// particular server-man process lifetime.
	restartedManager:=NewFirecracker(store,nil)
	restartedManager.callHost=call
	obs,err=restartedManager.readHost(ctx,c,key)
	if err!=nil || !obs.Running {t.Fatalf("fresh adapter lost running VM: %+v %v",obs,err)}
	boot:=c
	boot.Services=map[string]Service{id:service}
	enabled:=service
	enabled.Enabled=true
	boot.Services[id]=enabled
	if err:=backend.Apply(ctx,c,boot,key,"next_start");err!=nil{t.Fatal("enable boot:",err)}
	obs,err=backend.readHost(ctx,boot,key)
	if err!=nil || !obs.Running || !obs.Enabled || !obs.Matches {t.Fatalf("enable changed current execution: %+v %v",obs,err)}
	hardware:=enabled
	modified:=*enabled.VM
	modified.CPUs=2
	hardware.VM=&modified
	changed:=boot
	changed.Services=map[string]Service{id:hardware}
	if err:=backend.ValidateTransition(ctx,boot,changed,key);!errors.Is(err,errFirecrackerStoppedUpdate) {
		t.Fatalf("running hardware change was not rejected: %v",err)
	}
	if err:=backend.Action(ctx,boot,key,"stop");err!=nil{t.Fatal("guest did not shut down gracefully:",err)}
	obs,err=backend.readHost(ctx,boot,key)
	if err!=nil || obs.Running {t.Fatalf("graceful stop incomplete: %+v %v",obs,err)}
	if err:=backend.Action(ctx,boot,key,"start");err!=nil{t.Fatal("restart after graceful stop:",err)}
	checkHTTP()
	if err:=backend.Action(ctx,boot,key,"force_stop");err!=nil{t.Fatal("acknowledged force-stop:",err)}
	obs,err=backend.readHost(ctx,boot,key)
	if err!=nil || obs.Running {t.Fatalf("force stop incomplete: %+v %v",obs,err)}
	if err:=backend.Apply(ctx,boot,c,key,"next_start");err!=nil{t.Fatal("disable boot:",err)}
	obs,err=backend.readHost(ctx,c,key)
	if err!=nil || obs.Enabled || obs.Running || !obs.Matches {t.Fatalf("disable changed execution unexpectedly: %+v %v",obs,err)}
	if err:=backend.Apply(ctx,c,Config{Version:1},key,"now");err!=nil{t.Fatal("remove stopped VM:",err)}
	published=false
	removed,err:=backend.VerifyRemoved(ctx,c,key)
	if err!=nil || !removed {t.Fatalf("native definition still registered: %v %v",removed,err)}
	for _,path:=range []string{kernel,root} {
		if _,err:=os.Stat(path);err!=nil{t.Fatalf("removal deleted a guest image %s: %v",path,err)}
	}
	t.Log("native jailed Firecracker lifecycle, guest HTTP, boot enablement, and data-preserving removal passed")
}
