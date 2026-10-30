package plugin

import "testing"

func TestValidateJobsManifest(t *testing.T) {
	m := Manifest{
		Name: "jobs-test", Version: "1.0.0", HookAPI: 2, Entrypoint: "index.js",
		Permissions: []string{PermScheduleJobs},
		Jobs:        []JobSpec{{Name: "batch", TimeoutMS: 15000, MaxAttempts: 3}},
	}
	if err := validateV2Manifest(&m, "."); err != nil {
		t.Fatalf("good jobs manifest: %v", err)
	}
	noPerm := m
	noPerm.Permissions = nil
	if err := validateV2Manifest(&noPerm, "."); err == nil {
		t.Fatal("jobs without permission should fail")
	}
	noJobs := m
	noJobs.Jobs = nil
	if err := validateV2Manifest(&noJobs, "."); err == nil {
		t.Fatal("schedule permission without jobs should fail")
	}
	bad := m
	bad.Jobs = []JobSpec{{Name: "batch", TimeoutMS: 40000}}
	if err := validateV2Manifest(&bad, "."); err == nil {
		t.Fatal("out-of-range job timeout should fail")
	}
}

func TestValidateNetManifest(t *testing.T) {
	base := Manifest{
		Name: "net-test", Version: "1.0.0", HookAPI: 2, Entrypoint: "index.js",
		Permissions: []string{PermNetHTTP},
	}
	good := base
	good.Net = &NetSpec{HTTP: &HTTPNetSpec{
		Hosts:          []string{"api.mojang.com", "*.example.com"},
		AllowUserHosts: true, UserHostPaths: []string{"/skinlib/info/", "/textures/"},
		MaxBytes: 2 << 20, TimeoutMS: 5000,
	}}
	if err := validateV2Manifest(&good, "."); err != nil {
		t.Fatalf("good manifest: %v", err)
	}

	missing := base
	if err := validateV2Manifest(&missing, "."); err == nil {
		t.Fatal("net permission without block should fail")
	}

	noPerm := good
	noPerm.Permissions = nil
	if err := validateV2Manifest(&noPerm, "."); err == nil {
		t.Fatal("net block without permission should fail")
	}

	badPath := good
	badPath.Net = &NetSpec{HTTP: &HTTPNetSpec{
		Hosts: []string{"api.mojang.com"}, AllowUserHosts: true,
		UserHostPaths: []string{"skinlib/info"}, MaxBytes: 1024,
	}}
	if err := validateV2Manifest(&badPath, "."); err == nil {
		t.Fatal("relative outbound path should fail")
	}

	badHost := good
	badHost.Net = &NetSpec{HTTP: &HTTPNetSpec{Hosts: []string{"https://api.mojang.com"}, MaxBytes: 1024}}
	if err := validateV2Manifest(&badHost, "."); err == nil {
		t.Fatal("host with scheme should fail")
	}
}
