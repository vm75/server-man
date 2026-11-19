package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"server-man/internal/admin"
	"server-man/internal/core"
	"server-man/internal/firecrackerhost"
)

var errFirecrackerStoppedUpdate = errors.New("stop the Firecracker VM before changing native hardware or image references; Save for next start cannot defer active hardware changes")

// Firecracker uses the same declarative coordinator and registry tools as
// libvirt/Incus. Native KVM/TAP/jailer operations are delegated to the existing
// root-owned helper. There is no direct unjailed execution fallback.
type Firecracker struct {
	Store     Store
	Directory string
	Runner    core.Executor
	Helper    admin.Client
	callHost  func(context.Context, string, core.Input) (any, error) // isolated test seam; nil in production
}
func NewFirecracker(store Store, runner core.Executor) *Firecracker {
	return &Firecracker{Store:store,Directory:store.Path+".generated/firecracker",Runner:runner,Helper:admin.Client{Runner:runner}}
}
func (f *Firecracker) resourceDir(key string) string {
	_,id,_:=strings.Cut(key,"/")
	return filepath.Join(f.Directory,id)
}
func (f *Firecracker) spec(c Config,key string) (firecrackerhost.Spec,error) {
	_,id,ok:=strings.Cut(key,"/")
	s,exists:=c.Services[id]
	if !ok || !exists || s.Kind!="vm" || s.Backend!="firecracker" || s.VM==nil || s.VM.Firecracker==nil {
		return firecrackerhost.Spec{},errors.New("not a Firecracker VM")
	}
	v:=s.VM
	if err:=validateFirecrackerVM(c,id,*v);err!=nil{return firecrackerhost.Spec{},err}
	if v.UUID=="" || v.MAC=="" {return firecrackerhost.Spec{},errors.New("VM identity has not been persisted")}
	disks:=make([]string,0,len(v.Disks))
	for _,ref:=range v.Disks {disks=append(disks,f.Store.Resolve(c.Storage[ref].Path))}
	return firecrackerhost.Spec{
		ResourceID:id, UUID:v.UUID,Kernel:f.Store.Resolve(c.Storage[v.Firecracker.Kernel].Path),
		Disks:disks,Bridge:c.Bindings[v.Network].Name,MAC:v.MAC,
		CPUs:v.CPUs,MemoryMiB:v.MemoryMiB,BootArgs:v.Firecracker.BootArgs,Enabled:s.Enabled,
	},nil
}
func (f *Firecracker) request(ctx context.Context,operation string,values core.Input) (any,error) {
	if f.callHost != nil {return f.callHost(ctx,operation,values)}
	return f.Helper.Call(ctx,operation,values,io.Discard)
}
func (f *Firecracker) readHost(ctx context.Context,c Config,key string) (firecrackerhost.Observation,error) {
	spec,err:=f.spec(c,key)
	if err!=nil{return firecrackerhost.Observation{},err}
	raw,err:=f.request(ctx,"firecracker_inspect",core.Input{"id":spec.ResourceID,"spec":spec})
	if err!=nil{return firecrackerhost.Observation{},err}
	data,err:=json.Marshal(raw)
	if err!=nil{return firecrackerhost.Observation{},errors.New("invalid Firecracker inspection data")}
	var state firecrackerhost.Observation
	if json.Unmarshal(data,&state)!=nil {return state,errors.New("invalid Firecracker inspection result")}
	return state,nil
}
func (f *Firecracker) Validate(ctx context.Context,c Config,key string) error {
	if _,err:=RenderFirecracker(c,key);err!=nil{return err}
	spec,err:=f.spec(c,key)
	if err!=nil{return err}
	if _,err=f.request(ctx,"firecracker_validate",core.Input{"id":spec.ResourceID,"spec":spec});err!=nil {
		return errors.New("Firecracker host prerequisites or prepared images are unavailable")
	}
	return nil
}
func (f *Firecracker) Inspect(ctx context.Context,c Config,key string) (Observation,error) {
	native,err:=f.readHost(ctx,c,key)
	if err!=nil{return Observation{},err}
	return Observation{Registered:native.Registered,Active:native.Running,State:native.State,
		Matches:native.Matches,ActiveMatches:native.ActiveMatches},nil
}
func (f *Firecracker) ValidateTransition(ctx context.Context,previous,desired Config,key string) error {
	_,id,_:=strings.Cut(key,"/")
	old,exists:=previous.Services[id]
	if !exists || old.Backend!="firecracker" {return nil}
	if runtimeFingerprint(previous,key)==runtimeFingerprint(desired,key) {return nil}
	native,err:=f.readHost(ctx,previous,key)
	if err!=nil{return err}
	if native.Running {return errFirecrackerStoppedUpdate}
	return nil
}
func (f *Firecracker) definition(c Config,key string) (map[string][]byte,error) {
	native,err:=RenderFirecracker(c,key)
	if err!=nil{return nil,err}
	// YAML permits a comment followed by JSON object notation; the actual
	// secure host helper renders executable JSON independently from the spec.
	return map[string][]byte{"instance.yaml":append([]byte(ownedHeader),native...)},nil
}
func (f *Firecracker) Apply(ctx context.Context,previous,desired Config,key,mode string) error {
	_,id,_:=strings.Cut(key,"/")
	_,had:=previous.Services[id]
	_,exists:=desired.Services[id]
	before,after:=map[string][]byte{},map[string][]byte{}
	var err error
	if had {before,err=f.definition(previous,key);if err!=nil{return err}}
	if exists {after,err=f.definition(desired,key);if err!=nil{return err}}
	if err=checkArtifactOwnership(f.resourceDir(key),before,after);err!=nil{return err}
	if !exists {
		_,err=f.request(ctx,"firecracker_remove",core.Input{"id":id})
		if err!=nil{return errors.New("Firecracker VM removal was refused; stop and inspect native state first")}
		return publishArtifacts(f.resourceDir(key),before,after)
	}
	if err=f.ValidateTransition(ctx,previous,desired,key);err!=nil{return err}
	// Publication never starts a stopped guest, even for mode=now.
	if err=publishArtifacts(f.resourceDir(key),before,after);err!=nil{return err}
	spec,err:=f.spec(desired,key)
	if err!=nil{return err}
	if _,err=f.request(ctx,"firecracker_publish",core.Input{"id":id,"spec":spec});err!=nil {
		return errors.New("Firecracker native definition publication failed; inspect host service and retry")
	}
	return nil
}
func (f *Firecracker) VerifyRemoved(ctx context.Context,previous Config,key string)(bool,error) {
	_,id,_:=strings.Cut(key,"/")
	raw,err:=f.request(ctx,"firecracker_inspect",core.Input{"id":id})
	if err!=nil{return false,err}
	data,err:=json.Marshal(raw)
	if err!=nil{return false,err}
	var state firecrackerhost.Observation
	if err=json.Unmarshal(data,&state);err!=nil{return false,err}
	return !state.Registered&&!state.Running,nil
}
func (f *Firecracker) Action(ctx context.Context,c Config,key,operation string) error {
	spec,err:=f.spec(c,key)
	if err!=nil{return err}
	if operation!="start"&&operation!="stop"&&operation!="restart"&&operation!="force_stop" {return errors.New("unsupported Firecracker lifecycle action")}
	if _,err=f.request(ctx,"firecracker_action",core.Input{"id":spec.ResourceID,"action":operation});err!=nil {
		return errors.New("Firecracker lifecycle failed; check host service state and VM logs. A failed graceful stop never force-kills the guest")
	}
	return nil
}
func (f *Firecracker) Prerequisites(ctx context.Context) []Issue {
	issues:=[]Issue{}
	for _,item:=range []struct{path,field string}{
		{firecrackerhost.VMMPath,"firecracker"},
		{firecrackerhost.JailerPath,"jailer"},
		{firecrackerhost.RuntimePath,"host_runner"},
		{"/dev/kvm","kvm"},
		{"/sys/fs/cgroup/cgroup.controllers","cgroup_v2"},
	} {
		if _,err:=os.Stat(item.path);err!=nil {
			issues=append(issues,Issue{"error","backend/firecracker",item.field,"missing_dependency",
				"Firecracker host prerequisite is unavailable.", "Install and configure the backend and verify its jailer, KVM and cgroup v2 access."})
		}
	}
	return issues
}
func (f *Firecracker) ResourcePrerequisites(ctx context.Context,c Config,key string) []Issue {
	spec,err:=f.spec(c,key)
	if err!=nil {return []Issue{{"error",key,"definition","invalid_firecracker","Firecracker definition is invalid.","Repair the VM configuration."}}}
	if _,err=os.Stat(filepath.Join("/sys/class/net",spec.Bridge,"bridge"));err!=nil {
		return []Issue{{"error",key,"bridge","missing_bridge","The requested Linux bridge is unavailable.","Create/configure the bridge outside server-man, then recheck."}}
	}
	if _,err=f.request(ctx,"firecracker_validate",core.Input{"id":spec.ResourceID,"spec":spec});err!=nil {
		return []Issue{{"error",key,"runtime","host_setup_unavailable","Secure Firecracker host preparation is unavailable.","Verify sudo helper, prepared images, KVM access, jailer and systemd."}}
	}
	return nil
}
func (f *Firecracker) Logs(ctx context.Context,c Config,key string,tail int)(ManagedLogs,error) {
	spec,err:=f.spec(c,key)
	if err!=nil{return ManagedLogs{},err}
	raw,err:=f.request(ctx,"firecracker_logs",core.Input{"id":spec.ResourceID,"tail":tail})
	if err!=nil{return ManagedLogs{},core.Unavailable("Firecracker native logs are unavailable")}
	data,err:=json.Marshal(raw)
	if err!=nil{return ManagedLogs{},err}
	var result firecrackerhost.LogResult
	if err=json.Unmarshal(data,&result);err!=nil{return ManagedLogs{},err}
	return ManagedLogs{Text:result.Text,Truncated:result.Truncated,Source:result.Source},nil
}
