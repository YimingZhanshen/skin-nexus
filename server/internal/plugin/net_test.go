package plugin

import "testing"

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
