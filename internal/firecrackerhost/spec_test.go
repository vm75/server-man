package firecrackerhost

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func exampleSpec() Spec {
	return Spec{
		ResourceID:"api",UUID:"11111111-2222-4222-8222-333333333333",
		Kernel:"/home/example/assets/vmlinux",
		Disks:[]string{"/home/example/data/root.ext4","/home/example/data/logs.raw"},
		Bridge:"br-microvm",MAC:"02:00:00:00:12:34",CPUs:2,MemoryMiB:512,Enabled:false,
		UID:1000,GID:1000,
	}
}
func TestNativeSpecAndSystemdUnit(t *testing.T) {
	spec:=exampleSpec()
	if err:=spec.Validate();err!=nil {t.Fatal(err)}
	id:=Identity(spec.UID,spec.ResourceID)
	if len(id)!=22 || !strings.HasPrefix(id,"fc") {t.Fatal("unexpected stable native ID",id)}
	if UnitName(id)!="sm-firecracker-"+id+".service" {t.Fatal("unstable native unit")}
	if TapName(spec.UUID)!=TapName(spec.UUID) || len(TapName(spec.UUID))>15 {t.Fatal("unstable TAP identity")}
	raw,err:=spec.NativeJSON()
	if err!=nil {t.Fatal(err)}
	var native struct {
		Boot map[string]any `json:"boot-source"`
		Machine map[string]any `json:"machine-config"`
		Drives []map[string]any `json:"drives"`
		Interfaces []map[string]any `json:"network-interfaces"`
	}
	if err=json.Unmarshal(raw,&native);err!=nil {t.Fatal(err)}
	if native.Boot["kernel_image_path"]!="/kernel" || native.Machine["vcpu_count"]!=float64(2) ||
		len(native.Drives)!=2 || native.Drives[0]["path_on_host"]!="/disks/root" ||
		native.Interfaces[0]["host_dev_name"]!=TapName(spec.UUID) {t.Fatalf("bad native config: %#v",native)}
	next,err:=spec.NativeJSON()
	if err!=nil || !bytes.Equal(raw,next) {t.Fatal("unstable native config",err)}
	unit,err:=spec.Unit()
	if err!=nil {t.Fatal(err)}
	for _,text:=range []string{header,"ExecStartPre="+RuntimePath+" prepare "+id,"ExecStopPost="+RuntimePath+" cleanup "+id,"--chroot-base-dir "+BasePath+"/jails","--config-file /vm.json","Restart=no","WantedBy=multi-user.target"} {
		if !strings.Contains(string(unit),text) {t.Fatalf("missing secure unit directive %q",text)}
	}
	if strings.Contains(string(unit),spec.Kernel) || strings.Contains(string(unit),spec.Disks[0]) {
		t.Fatal("source path leaked into privileged systemd command")
	}
}
func TestRejectUnsafeNativeSpecs(t *testing.T) {
	base:=exampleSpec()
	cases:=[]struct{name string;change func(*Spec)}{
		{"wrong id",func(s *Spec){s.ResourceID="../../etc"}},
		{"wrong uid",func(s *Spec){s.UID=0}},
		{"bad uuid",func(s *Spec){s.UUID="not-uuid"}},
		{"bad bridge",func(s *Spec){s.Bridge="; reboot"}},
		{"long bridge",func(s *Spec){s.Bridge=strings.Repeat("x",20)}},
		{"multicast mac",func(s *Spec){s.MAC="01:00:00:00:00:00"}},
		{"bad cpus",func(s *Spec){s.CPUs=0}},
		{"bad memory",func(s *Spec){s.MemoryMiB=64}},
		{"wrong path",func(s *Spec){s.Kernel="relative/vmlinux"}},
		{"unclean path",func(s *Spec){s.Disks[0]="/var/../etc/shadow"}},
		{"duplicate disk",func(s *Spec){s.Disks[0]=s.Kernel}},
		{"bad boot args",func(s *Spec){s.BootArgs="root=/dev/vda\nbad"}},
	}
	for _,tc:=range cases {
		t.Run(tc.name,func(t *testing.T){
			spec:=base
			spec.Disks=append([]string(nil),base.Disks...)
			tc.change(&spec)
			if spec.Validate()==nil {t.Fatal("unsafe Firecracker spec accepted")}
		})
	}
}
func TestRootHelperRequestBounds(t *testing.T) {
	for _,input:=range []string{
		`{"id":"../evil"}`,`{"id":"api","unexpected":true}`,
		`{"id":"api"}{"id":"other"}`,strings.Repeat("x",33<<10),
	} {
		if _,err:=inputRequest(json.RawMessage(input));err==nil {t.Fatal("accepted unsafe native request")}
	}
	if _,err:=inputRequest(json.RawMessage(`{"id":"api","action":"start"}`));err!=nil {t.Fatal(err)}
	if filepath.IsAbs(Root(Identity(1000,"api")))==false {t.Fatal("native state root not absolute")}
}
