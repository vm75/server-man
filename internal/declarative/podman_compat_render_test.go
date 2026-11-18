package declarative

import (
	"context"
	"strings"
	"server-man/internal/core"
	"testing"
)

// The same typed application definition drives both native Podman formats.
// These tests never execute containers or modify host permissions or labels.
func TestPodmanCommonRuntimeRenderers(t *testing.T) {
	for _, backend := range []string{"quadlet", "compose"} {
		t.Run(backend, func(t *testing.T) {
			c, cfg, _ := coordinatorFixture(t)
			s := cfg.Services["website"]
			s.Backend = backend
			v := s.Containers["web"]
			v.User = "0:0"
			v.UserNSMode = "keep-id:uid=1000,gid=1000"
			v.Devices = []string{"/dev/dri:/dev/dri"}
			v.CapAdd = []string{"NET_ADMIN"}
			v.CapDrop = []string{"ALL"}
			v.SecurityOpt = []string{"label=disable", "seccomp:unconfined"}
			v.ReadOnly = true
			v.Init = true
			v.TTY = true
			v.Tmpfs = []string{"/tmp/cache:size=1000000"}
			v.ShmSize = "256mb"
			v.StopGracePeriod = "30s"
			v.PullPolicy = "missing"
			v.WorkingDir = "/data"
			v.Entrypoint = []string{"/bin/sh", "-c"}
			v.LogDriver = "k8s-file"
			v.LogOptions = map[string]string{"max-size": "100k"}
			v.Healthcheck = &ContainerHealth{Test: []string{"CMD-SHELL", "test -f /data/ready"}, Interval: "10s", Retries: 3}
			v.Mounts[0].SELinux = "private"
			v.Mounts[0].Chown = true
			s.Containers["web"] = v
			cfg.Services["website"] = s
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			if backend == "quadlet" {
				files, err := RenderQuadlet(c.Store, cfg, "service/website")
				if err != nil { t.Fatal(err) }
				rendered := string(files[quadletBase("website", "web")+".container"])
				for _, expected := range []string{
					"User=0\n", "Group=0\n", "UserNS=keep-id:uid=1000,gid=1000\n",
					"AddDevice=/dev/dri:/dev/dri\n", "AddCapability=NET_ADMIN\n",
					"DropCapability=ALL\n", "SecurityLabelDisable=true\n",
					"SeccompProfile=unconfined\n", "Volume="+c.Store.Resolve(cfg.Storage["content"].Path)+":/srv/content:rw,U,Z\n",
					"ReadOnly=true\n", "RunInit=true\n", "ShmSize=256m\n", "Tmpfs=/tmp/cache:size=1000000\n",
					"StopTimeout=30\n", "LogDriver=k8s-file\n", "LogOpt=max-size=100k\n",
					"HealthInterval=10s\n", "HealthRetries=3\n", "WorkingDir=/data\n",
				} {
					if !strings.Contains(rendered, expected) { t.Errorf("Quadlet missing %q in:\n%s", expected, rendered) }
				}
			} else {
				renderedBytes, err := RenderCompose(c.Store, cfg, "service/website")
				if err != nil { t.Fatal(err) }
				rendered := string(renderedBytes)
				for _, expected := range []string{
					"x-podman:", "in_pod: false", "userns_mode: keep-id:uid=1000,gid=1000",
					"x-podman.uidmaps", // asserted absent below: explicit mappings are mutually exclusive with keep-id
					"read_only: true", "security_opt:", "k8s-file", "max-size: 100k",
					":/srv/content:rw,U,Z",
				} {
					if expected == "x-podman.uidmaps" {
						if strings.Contains(rendered, expected) { t.Errorf("unexpected explicit mapping with keep-id:\n%s", rendered) }
						continue
					}
					if !strings.Contains(rendered, expected) { t.Errorf("Compose missing %q in:\n%s", expected, rendered) }
				}
			}
		})
	}
}

func TestPodmanCustomMappingsAndHealthDependencies(t *testing.T) {
	for _, backend := range []string{"quadlet", "compose"} {
		t.Run(backend, func(t *testing.T) {
			c, cfg, _ := coordinatorFixture(t)
			s := cfg.Services["website"]
			s.Backend = backend
			s.Containers["db"] = Container{
				Image: "docker.io/library/postgres:15",
				Healthcheck: &ContainerHealth{Test: []string{"CMD-SHELL", "pg_isready"}, Interval: "10s", Retries: 10},
			}
			web := s.Containers["web"]
			web.Ports = nil
			web.DependsOn = map[string]string{"db": "healthy"}
			web.NetworkMode = "service:db"
			web.UIDMaps = []string{"1000:0:1", "0:1:1000", "1001:1001:64536"}
			s.Containers["web"] = web
			cfg.Services["website"] = s
			if err := cfg.Validate(); err != nil { t.Fatal(err) }
			if backend == "quadlet" {
				rendered, err := RenderQuadlet(c.Store, cfg, "service/website")
				if err != nil { t.Fatal(err) }
				webData := string(rendered[quadletBase("website", "web")+".container"])
				dbData := string(rendered[quadletBase("website", "db")+".container"])
				for _, value := range []string{
					"Network="+quadletBase("website", "db")+".container",
					"Requires="+quadletBase("website", "db")+".service",
					"UIDMap=1000:0:1",
				} {
					if !strings.Contains(webData, value) { t.Errorf("Quadlet web missing %q: %s", value, webData) }
				}
				if !strings.Contains(dbData, "Notify=healthy\n") { t.Error("Quadlet prerequisite not gated on health") }
			} else {
				rendered, err := RenderCompose(c.Store, cfg, "service/website")
				if err != nil { t.Fatal(err) }
				for _, value := range []string{"x-podman.uidmaps:", "network_mode: service:db", "condition: service_healthy", "in_pod: false"} {
					if !strings.Contains(string(rendered), value) { t.Errorf("Compose missing %q in:\n%s", value, rendered) }
				}
			}
		})
	}
}

func TestPodmanRejectsDangerousOrUnrepresentableOptions(t *testing.T) {
	cases := []struct {
		name string
		apply func(*Container)
	}{
		{"bad uid map", func(v *Container) { v.UIDMaps = []string{"1000:0:0"} }},
		{"uid map with keep-id", func(v *Container) { v.UIDMaps = []string{"1000:0:1"}; v.UserNSMode = "keep-id" }},
		{"bad device path", func(v *Container) { v.Devices = []string{"relative:/dev/dri"} }},
		{"unsupported security option", func(v *Container) { v.SecurityOpt = []string{"some-unknown-flag"} }},
		{"unknown SELinux mode", func(v *Container) { v.Mounts[0].SELinux = "unconfined" }},
		{"unknown peer", func(v *Container) { v.NetworkMode = "service:absent" }},
		{"unsupported health duration", func(v *Container) { v.Healthcheck = &ContainerHealth{Test: []string{"CMD-SHELL","true"}, Interval:"tomorrow"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, cfg, _ := coordinatorFixture(t)
			s := cfg.Services["website"]
			v := s.Containers["web"]
			tc.apply(&v)
			s.Containers["web"] = v
			cfg.Services["website"] = s
			if err := cfg.Validate(); err == nil { t.Fatal("invalid container options were accepted") }
		})
	}
}

func TestComposeRestartDoesNotOverrideIndependentBootPolicy(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	s := cfg.Services["website"]
	s.Backend = "compose"
	v := s.Containers["web"]
	v.Restart = "unless-stopped"
	s.Containers["web"] = v
	cfg.Services["website"] = s
	if err := cfg.Validate(); err == nil {
		t.Fatal("Compose would restart containers independently of disabled boot policy")
	}
	v.Restart = "no"
	s.Containers["web"] = v
	cfg.Services["website"] = s
	if err := cfg.Validate(); err != nil { t.Fatal(err) }
	out, err := RenderCompose(c.Store, cfg, "service/website")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(out), "restart: \"no\"") && !strings.Contains(string(out), "restart: \"no\"\n") {
		t.Fatalf("Compose restart guard missing:\n%s", out)
	}
}

func TestNativeAdvancedPodmanQuadletGenerator(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	q := NewQuadlet(c.Store, core.Runner{})
	if q.Generator == "" { t.Skip("Quadlet generator is not installed") }
	s := cfg.Services["website"]
	v := s.Containers["web"]
	v.ContainerName = "managed-web"
	v.UserNSMode = "keep-id"
	v.Mounts[0].SELinux = "private"
	v.Mounts[0].Chown = true
	v.ReadOnly = true
	v.Init = true
	v.ShmSize = "256mb"
	v.LogDriver = "k8s-file"
	v.LogOptions = map[string]string{"max-size":"100k"}
	v.Healthcheck = &ContainerHealth{Test:[]string{"CMD-SHELL","test -f /srv/content"},Interval:"10s",Retries:3}
	s.Containers["web"] = v
	cfg.Services["website"] = s
	if err := q.Validate(context.Background(), cfg, "service/website"); err != nil {
		t.Fatalf("native Quadlet generator rejected runtime compatibility fields: %v", err)
	}
}

func TestPodmanExplicitContainerName(t *testing.T) {
	for _, backend := range []string{"quadlet","compose"} {
		t.Run(backend,func(t *testing.T) {
			c,cfg,_:=coordinatorFixture(t)
			s:=cfg.Services["website"]
			s.Backend=backend
			v:=s.Containers["web"]
			v.ContainerName="custom-managed-web"
			s.Containers["web"]=v
			cfg.Services["website"]=s
			var generated string
			if backend=="quadlet" {
				files,err:=RenderQuadlet(c.Store,cfg,"service/website")
				if err!=nil{t.Fatal(err)}
				generated=string(files[quadletBase("website","web")+".container"])
				if !strings.Contains(generated,"ContainerName=custom-managed-web\n") {t.Fatal(generated)}
			}else{
				data,err:=RenderCompose(c.Store,cfg,"service/website")
				if err!=nil{t.Fatal(err)}
				generated=string(data)
				if !strings.Contains(generated,"container_name: custom-managed-web") {t.Fatal(generated)}
			}
		})
	}
}

func TestComposeShortBindRequiresUnambiguousPaths(t *testing.T) {
	c,cfg,_:=coordinatorFixture(t)
	s:=cfg.Services["website"]
	s.Backend="compose"
	v:=s.Containers["web"]
	v.Mounts[0].SELinux="private"
	s.Containers["web"]=v
	cfg.Services["website"]=s
	st:=cfg.Storage["content"]
	st.Path="data/colon:path"
	cfg.Storage["content"]=st
	if _,err:=RenderCompose(c.Store,cfg,"service/website");err==nil {
		t.Fatal("Compose relabeling mount accepted a colon in the host path")
	}
}
