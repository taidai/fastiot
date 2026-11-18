package authz

import (
	"reflect"
	"testing"
)

func TestResolveRoleDefaults(t *testing.T) {
	admin := Resolve(true, "viewer", nil)
	if !admin.Allowed(AccountManage) || !admin.Allowed(Export) || len(admin.List()) != len(All) {
		t.Fatalf("admin set = %v", admin.List())
	}
	implementer := Resolve(false, "implementer", nil)
	for _, action := range []string{Configure, Export, Delete, WriteValue, VirtualInputWrite} {
		if !implementer.Allowed(action) {
			t.Fatalf("implementer missing %s: %v", action, implementer.List())
		}
	}
	if implementer.Allowed(AccountManage) || implementer.Allowed(AuditView) {
		t.Fatalf("implementer received admin actions: %v", implementer.List())
	}
	operator := Resolve(false, "operator", nil)
	if !operator.Allowed(WriteValue) || operator.Allowed(Configure) || operator.Allowed(Export) {
		t.Fatalf("operator set = %v", operator.List())
	}
	viewer := Resolve(false, "viewer", nil)
	if len(viewer.List()) != 0 {
		t.Fatalf("viewer set = %v", viewer.List())
	}
}

func TestResolveWhitelistOverridesRole(t *testing.T) {
	set := Resolve(false, "implementer", []string{Export})
	if !set.Allowed(Export) || set.Allowed(Configure) || len(set.List()) != 1 {
		t.Fatalf("whitelist set = %v", set.List())
	}
	if !Resolve(false, "implementer", []string{Export}).Allowed(Export) {
		t.Fatal("explicit export grant lost")
	}
}

func TestListUsesCanonicalOrder(t *testing.T) {
	set := Resolve(false, "viewer", []string{Export, Configure})
	if got, want := set.List(), []string{Configure, Export}; !reflect.DeepEqual(got, want) {
		t.Fatalf("list = %v, want %v", got, want)
	}
}
