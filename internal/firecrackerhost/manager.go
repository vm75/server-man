package firecrackerhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Request struct {
	ID     string `json:"id"`
	Action string `json:"action,omitempty"`
	Tail   int    `json:"tail,omitempty"`
	Spec   *Spec  `json:"spec,omitempty"`
}
type LogResult struct {
	Text string `json:"text"`
	Truncated bool `json:"truncated"`
	Source string `json:"source"`
}
func specHash(spec Spec) string {
	data,_:=json.Marshal(spec)
	h:=sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
func runtimeHash(spec Spec) string {
	spec.Enabled = false
	return specHash(spec)
}
type publicationIntent struct {
	DesiredHash string `json:"desired_hash"`
	Previous *Spec `json:"previous,omitempty"`
}
func readIntent(id string) (publicationIntent,bool,error) {
	data,err:=regularRootFile(filepath.Join(Root(id),"publication.json"))
	if os.IsNotExist(err) {return publicationIntent{},false,nil}
	if err!=nil{return publicationIntent{},false,err}
	var intent publicationIntent
	dec:=json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&intent)!=nil || dec.Decode(new(any))!=io.EOF || len(intent.DesiredHash)!=64 {
		return intent,false,errors.New("invalid Firecracker interrupted-publication record")
	}
	if intent.Previous!=nil {
		if intent.Previous.Validate()!=nil || Identity(intent.Previous.UID,intent.Previous.ResourceID)!=id {
			return intent,false,errors.New("invalid previous Firecracker publication")
		}
	}
	return intent,true,nil
}
func writeIntent(id string,intent publicationIntent) error {
	data,err:=json.MarshalIndent(intent,"","  ")
	if err!=nil{return err}
	return writeRootFile(filepath.Join(Root(id),"publication.json"),append(data,'\n'),0600)
}
func inputRequest(raw json.RawMessage) (Request,error) {
	if len(raw)>32<<10 {return Request{},errors.New("Firecracker request exceeds limit")}
	dec:=json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var req Request
	if err:=dec.Decode(&req);err!=nil{return req,errors.New("invalid Firecracker request")}
	if dec.Decode(new(any))!=io.EOF {return req,errors.New("extra Firecracker request data")}
	if !idPattern.MatchString(req.ID) {return req,errors.New("invalid Firecracker service ID")}
	return req,nil
}
func (r Request) nativeIdentity(uid int) string { return Identity(uid,r.ID) }
func validateRequested(uid,gid int,req Request) error {
	if req.Spec==nil || req.Spec.ResourceID!=req.ID || req.Spec.UID!=0 || req.Spec.GID!=0 {
		return errors.New("invalid Firecracker validation request")
	}
	spec:=*req.Spec
	spec.UID,spec.GID=uid,gid
	return checkSources(spec)
}
func verifyOwnedUnit(id string, old, desired []byte) error {
	raw,err:=regularRootFile(UnitPath(id))
	if os.IsNotExist(err) {
		if old==nil {return nil}
		return errors.New("previously owned Firecracker unit is missing")
	}
	if err!=nil {return err}
	if old!=nil && bytes.Equal(raw,old) {return nil}
	if desired!=nil && bytes.Equal(raw,desired) {return nil}
	return errors.New("Firecracker unit differs from the owned definition")
}
func unitStatus(ctx context.Context,id string) (bool,bool,error) {
	// No unit state is inferred from a stale API socket.
	output,err:=runCommand(ctx,Systemctl,"show",UnitName(id),"--property=ActiveState,UnitFileState","--no-pager")
	if err!=nil{return false,false,err}
	state,fileState:="",""
	for _,line:=range strings.Split(output,"\n") {
		key,value,ok:=strings.Cut(line,"=")
		if !ok {continue}
		if key=="ActiveState" {state=value}
		if key=="UnitFileState" {fileState=value}
	}
	switch state {
	case "active","activating","reloading":
	case "inactive","failed":
	case "deactivating":return true,false,nil
	default:return false,false,errors.New("Firecracker unit status is ambiguous")
	}
	return state=="active" || state=="activating" || state=="reloading",fileState=="enabled",nil
}
func inspect(ctx context.Context, uid int, req Request) (Observation,error) {
	id:=req.nativeIdentity(uid)
	spec,err:=readSpec(id)
	if os.IsNotExist(err) {
		if _,e:=os.Lstat(UnitPath(id));e==nil {return Observation{},errors.New("untracked Firecracker systemd unit exists")}
		return Observation{State:"stopped"},nil
	}
	if err!=nil {return Observation{},err}
	unit,err:=spec.Unit()
	if err!=nil{return Observation{},err}
	if err=verifyOwnedUnit(id,unit,unit);err!=nil{return Observation{},err}
	running,enabled,err:=unitStatus(ctx,id)
	if err!=nil{return Observation{},err}
	result:=Observation{Registered:true,Running:running,State:"stopped",Enabled:enabled}
	if running {result.State="running"}
	if req.Spec==nil {
		result.Matches=true
	} else {
		desired:=*req.Spec
		desired.UID=spec.UID
		desired.GID=spec.GID
		if desired.Validate()!=nil{return Observation{},errors.New("invalid requested Firecracker definition")}
		a,_:=json.Marshal(spec)
		b,_:=json.Marshal(desired)
		result.Matches=bytes.Equal(a,b) && enabled==desired.Enabled
	}
	if _,pending,e:=readIntent(id);e!=nil {return Observation{},e} else if pending {result.Matches=false}
	if _,removing,e:=readRemoval(id);e!=nil {return Observation{},e} else if removing {result.Matches=false}
	if running {
		marker,err:=regularRootFile(filepath.Join(Root(id),"started.hash"))
		result.ActiveMatches=err==nil && strings.TrimSpace(string(marker))==runtimeHash(spec)
	} else {
		result.ActiveMatches=true
	}
	return result,nil
}
func publish(ctx context.Context, uid,gid int, req Request) (Observation,error) {
	if req.Spec==nil || req.Spec.UID!=0 || req.Spec.GID!=0 {return Observation{},errors.New("invalid Firecracker publish identity")}
	spec:=*req.Spec
	if spec.ResourceID!=req.ID {return Observation{},errors.New("mismatched Firecracker resource")}
	spec.UID,spec.GID=uid,gid
	if err:=spec.Validate();err!=nil{return Observation{},err}
	id:=req.nativeIdentity(uid)
	if _,removing,e:=readRemoval(id);e!=nil{return Observation{},e} else if removing {
		return Observation{},errors.New("complete interrupted Firecracker removal before publishing")
	}
	current,err:=readSpec(id)
	had:=err==nil
	if err!=nil && !os.IsNotExist(err) {return Observation{},err}
	intent,pending,err:=readIntent(id)
	if err!=nil {return Observation{},err}
	if pending && intent.DesiredHash!=specHash(spec) {
		return Observation{},errors.New("another Firecracker native publication is incomplete; inspect and retry it")
	}
	var oldUnit []byte
	if pending {
		if intent.Previous!=nil {
			oldUnit,err=intent.Previous.Unit()
			if err!=nil{return Observation{},err}
		}
	} else if had {
		oldUnit,err=current.Unit()
		if err!=nil{return Observation{},err}
	}
	nextUnit,err:=spec.Unit()
	if err!=nil{return Observation{},err}
	if err=verifyOwnedUnit(id,oldUnit,nextUnit);err!=nil{return Observation{},err}
	running:=false
	if had {
		var e error
		running,_,e=unitStatus(ctx,id)
		if e!=nil{return Observation{},e}
		if running && !sameRuntime(current,spec) {
			return Observation{},errors.New("stop Firecracker VM before changing its native hardware")
		}
	}
	// Never conflate independent boot policy with current execution: a pure
	// Enable/Disable is safe even if a running VM's disk is temporarily missing.
	if !had || !running || !sameRuntime(current,spec) {
		if err=checkSources(spec);err!=nil{return Observation{},err}
	}
	if !pending {
		fresh:=publicationIntent{DesiredHash:specHash(spec)}
		if had {fresh.Previous=&current}
		if err=writeIntent(id,fresh);err!=nil{return Observation{},err}
	}
	if err=saveSpec(spec);err!=nil{return Observation{},err}
	if err=writeUnitFile(UnitPath(id),nextUnit);err!=nil{return Observation{},err}
	if _,err=runCommand(ctx,Systemctl,"daemon-reload");err!=nil{return Observation{},err}
	action:="disable"
	if spec.Enabled {action="enable"}
	if _,err=runCommand(ctx,Systemctl,action,UnitName(id));err!=nil{return Observation{},err}
	if err=os.Remove(filepath.Join(Root(id),"publication.json"));err!=nil && !os.IsNotExist(err) {return Observation{},err}
	return inspect(ctx,uid,Request{ID:req.ID,Spec:req.Spec})
}
func guestInfo(ctx context.Context,id string) (string,error) {
	socket:=filepath.Join(JailRoot(id),"api.socket")
	dialer:=&net.Dialer{Timeout:2*time.Second}
	client:=http.Client{Timeout:3*time.Second,Transport:&http.Transport{
		DisableKeepAlives:true,
		DialContext:func(ctx context.Context,_,_ string)(net.Conn,error){return dialer.DialContext(ctx,"unix",socket)},
	}}
	req,err:=http.NewRequestWithContext(ctx,http.MethodGet,"http://firecracker/",nil)
	if err!=nil{return "",err}
	resp,err:=client.Do(req)
	if err!=nil{return "",err}
	defer resp.Body.Close()
	if resp.StatusCode!=http.StatusOK{return "",errors.New("Firecracker instance metadata is unavailable")}
	data,err:=io.ReadAll(io.LimitReader(resp.Body,8193))
	if err!=nil || len(data)>8192{return "",errors.New("invalid Firecracker instance metadata")}
	var info struct{State string `json:"state"`}
	if json.Unmarshal(data,&info)!=nil{return "",errors.New("malformed Firecracker instance metadata")}
	return info.State,nil
}
func waitRunning(ctx context.Context,id string) error {
	timer:=time.NewTimer(30*time.Second)
	defer timer.Stop()
	tick:=time.NewTicker(500*time.Millisecond)
	defer tick.Stop()
	for {
		running,err:=isRunning(ctx,id)
		if err==nil && !running {return errors.New("Firecracker process exited before guest startup")}
		if err==nil && running {
			state,e:=guestInfo(ctx,id)
			if e==nil && state=="Running" {return nil}
		}
		select {
		case <-ctx.Done():return ctx.Err()
		case <-timer.C:return errors.New("Firecracker did not report a running guest")
		case <-tick.C:
		}
	}
}
func shutdown(ctx context.Context,id string) error {
	socket:=filepath.Join(JailRoot(id),"api.socket")
	dialer:=&net.Dialer{Timeout:2*time.Second}
	client:=http.Client{Timeout:5*time.Second,Transport:&http.Transport{
		DisableKeepAlives:true,
		DialContext:func(ctx context.Context,_,_ string)(net.Conn,error){return dialer.DialContext(ctx,"unix",socket)},
	}}
	request,err:=http.NewRequestWithContext(ctx,http.MethodPut,"http://firecracker/actions",strings.NewReader(`{"action_type":"SendCtrlAltDel"}`))
	if err!=nil{return errors.New("failed to construct Firecracker shutdown request")}
	request.Header.Set("Content-Type","application/json")
	response,err:=client.Do(request)
	if err!=nil{return errors.New("Firecracker guest shutdown API is not reachable")}
	defer response.Body.Close()
	if response.StatusCode<200 || response.StatusCode>299 {return errors.New("Firecracker guest rejected orderly shutdown")}
	return nil
}
func action(ctx context.Context,uid int, req Request) (Observation,error) {
	id:=req.nativeIdentity(uid)
	if _,removing,e:=readRemoval(id);e!=nil{return Observation{},e} else if removing {
		return Observation{},errors.New("Firecracker removal is incomplete; lifecycle changes are blocked")
	}
	spec,err:=readSpec(id)
	if err!=nil{return Observation{},errors.New("Firecracker native definition is missing")}
	unit,err:=spec.Unit()
	if err!=nil{return Observation{},err}
	if err=verifyOwnedUnit(id,unit,unit);err!=nil{return Observation{},err}
	running,_,err:=unitStatus(ctx,id)
	if err!=nil{return Observation{},err}
	if req.Action!="start" && req.Action!="stop" && req.Action!="restart" && req.Action!="force_stop" {return Observation{},errors.New("unsupported Firecracker action")}
	if req.Action=="force_stop" {
		if running {
			// Bypass the normal ExecStop guest-shutdown hook: this is the
			// separately acknowledged destructive operation.
			if _,err=runCommand(ctx,Systemctl,"kill","--kill-who=main","--signal=SIGKILL",UnitName(id));err!=nil{return Observation{},err}
			if _,err=runCommand(ctx,Systemctl,"stop",UnitName(id));err!=nil{return Observation{},err}
		}
		return inspect(ctx,uid,Request{ID:req.ID})
	}
	if req.Action=="stop" || req.Action=="restart" {
		if running {
			if err=shutdown(ctx,id);err!=nil{return Observation{},err}
			deadline:=time.NewTimer(90*time.Second)
			defer deadline.Stop()
			tick:=time.NewTicker(time.Second)
			defer tick.Stop()
		wait:
			for {
				running,err=isRunning(ctx,id)
				if err!=nil{return Observation{},err}
				if !running {break wait}
				select {
				case <-deadline.C:return Observation{},errors.New("guest did not shut down; no force-stop was executed")
				case <-ctx.Done():return Observation{},ctx.Err()
				case <-tick.C:
				}
			}
		}
	}
	if req.Action=="start" || req.Action=="restart" {
		if req.Action=="restart" || !running {
			if err=checkSources(spec);err!=nil{return Observation{},err}
			if _,err=runCommand(ctx,Systemctl,"start",UnitName(id));err!=nil{return Observation{},err}
			if err=waitRunning(ctx,id);err!=nil{return Observation{},err}
		}
	}
	return inspect(ctx,uid,Request{ID:req.ID})
}
// A removal marker is retained until the systemd unit and root-owned metadata
// have both been retired. It permits retry after an interrupted remove without
// trusting a newly created or foreign service file.
type removalIntent struct {
	Spec Spec `json:"spec"`
}
func readRemoval(id string) (removalIntent,bool,error) {
	data,err:=regularRootFile(filepath.Join(Root(id),"removal.json"))
	if os.IsNotExist(err) {return removalIntent{},false,nil}
	if err!=nil{return removalIntent{},false,err}
	decoder:=json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var intent removalIntent
	if decoder.Decode(&intent)!=nil || decoder.Decode(new(any))!=io.EOF ||
		intent.Spec.Validate()!=nil || Identity(intent.Spec.UID,intent.Spec.ResourceID)!=id {
		return removalIntent{},false,errors.New("invalid interrupted Firecracker removal record")
	}
	return intent,true,nil
}
func remove(ctx context.Context,uid int,req Request) (Observation,error) {
	id:=req.nativeIdentity(uid)
	pending,present,err:=readRemoval(id)
	if err!=nil {return Observation{},err}
	spec,err:=readSpec(id)
	had:=err==nil
	if err!=nil && !os.IsNotExist(err) {return Observation{},err}
	if !had && !present {
		if _,e:=os.Lstat(UnitPath(id));e==nil {
			return Observation{},errors.New("untracked systemd unit cannot be removed")
		}
		return Observation{State:"stopped"},nil
	}
	if !had {spec=pending.Spec}
	if present && specHash(spec)!=specHash(pending.Spec) {
		return Observation{},errors.New("interrupted Firecracker removal identity changed")
	}
	unit,err:=spec.Unit()
	if err!=nil{return Observation{},err}
	raw,e:=regularRootFile(UnitPath(id))
	unitExists:=e==nil
	if e!=nil && !os.IsNotExist(e) {return Observation{},e}
	if unitExists && !bytes.Equal(raw,unit) {
		return Observation{},errors.New("Firecracker unit differs from the owned definition")
	}
	if _,saving,e:=readIntent(id);e!=nil {return Observation{},e} else if saving {
		return Observation{},errors.New("complete interrupted Firecracker publication before removing it")
	}
	if unitExists {
		running,_,e:=unitStatus(ctx,id)
		if e!=nil{return Observation{},e}
		if running {return Observation{},errors.New("stop Firecracker before removing its definition")}
	}
	if !present {
		removalIntent:=removalIntent{Spec:spec}
		data,e:=json.Marshal(removalIntent)
		if e!=nil{return Observation{},e}
		if e=writeRootFile(filepath.Join(Root(id),"removal.json"),append(data,'\n'),0600);e!=nil {
			return Observation{},e
		}
	}
	if had {
		if err=Cleanup(id);err!=nil{return Observation{},err}
	}
	if unitExists {
		if _,err=runCommand(ctx,Systemctl,"disable",UnitName(id));err!=nil{return Observation{},err}
		if err=os.Remove(UnitPath(id));err!=nil && !os.IsNotExist(err) {return Observation{},err}
	}
	if _,err=runCommand(ctx,Systemctl,"daemon-reload");err!=nil{return Observation{},err}
	if had {
		if err=os.Remove(filepath.Join(Root(id),"spec.json"));err!=nil && !os.IsNotExist(err) {return Observation{},err}
	}
	_ = os.Remove(filepath.Join(Root(id),"started.hash"))
	if err=os.Remove(filepath.Join(Root(id),"removal.json"));err!=nil{return Observation{},err}
	if err=os.Remove(Root(id));err!=nil && !os.IsNotExist(err) {return Observation{},errors.New("native metadata remains; inspect root state")}
	return Observation{State:"stopped"},nil
}
func logs(ctx context.Context,uid int,req Request) (LogResult,error) {
	id:=req.nativeIdentity(uid)
	if _,err:=readSpec(id);err!=nil{return LogResult{},errors.New("unknown managed Firecracker VM")}
	if req.Tail<1 || req.Tail>2000{return LogResult{},errors.New("invalid Firecracker log limit")}
	output,truncated,err:=runCommandBounded(ctx,"/usr/bin/journalctl",128<<10,"--no-pager","--quiet","--output=short-iso","--lines",strconv.Itoa(req.Tail),"--unit",UnitName(id))
	if err!=nil{return LogResult{},err}
	return LogResult{Text:output,Truncated:truncated,Source:"firecracker-systemd"},nil
}

// Handle executes exclusively fixed root operations for one sudo caller.
// The trusted caller account cannot be selected through the JSON request.
func Handle(ctx context.Context,account,operation string,raw json.RawMessage)(any,error) {
	if os.Geteuid()!=0 {return nil,errors.New("Firecracker host helper requires root")}
	uid,gid,err:=accountIDs(account)
	if err!=nil{return nil,err}
	req,err:=inputRequest(raw)
	if err!=nil{return nil,err}
	switch operation {
	case "firecracker_validate":return map[string]bool{"valid":true},validateRequested(uid,gid,req)
	case "firecracker_inspect":return inspect(ctx,uid,req)
	case "firecracker_publish":return publish(ctx,uid,gid,req)
	case "firecracker_action":return action(ctx,uid,req)
	case "firecracker_remove":return remove(ctx,uid,req)
	case "firecracker_logs":return logs(ctx,uid,req)
	default:return nil,errors.New("unsupported Firecracker host operation")
	}
}
