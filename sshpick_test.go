package main

import "testing"

func TestFuzzyMatchSSHPick(t *testing.T) {
	items := []sshPickItem{
		{path: "folder1/onboarding-db-mst01", jump: true},
		{path: "folder1/onboarding-db2-mst02"},
		{path: "folder2/online-mst01"},
		{path: "folder2/online-mst02"},
		{path: "folder1/mobile-banking/legacy"},
	}
	got := filterSSHPick(items, "folder2/")
	if len(got) < 2 || got[0].item.path != "folder2/online-mst01" || got[1].item.path != "folder2/online-mst02" {
		t.Fatalf("folder2/ should rank the folder2 logins first, got %+v", got)
	}
	if got := filterSSHPick(items, "folder1 mst02"); len(got) != 1 || got[0].item.path != "folder1/onboarding-db2-mst02" {
		t.Fatalf("all terms must match, got %+v", got)
	}
	if got := filterSSHPick(items, "odb2"); len(got) == 0 || got[0].item.path != "folder1/onboarding-db2-mst02" {
		t.Fatalf("subsequence match failed, got %+v", got)
	}
	if got := filterSSHPick(items, "zzz"); len(got) != 0 {
		t.Fatalf("expected no matches, got %+v", got)
	}
	if got := filterSSHPick(items, ""); len(got) != len(items) {
		t.Fatalf("empty query should list all")
	}
}

func TestNumberLogins(t *testing.T) {
	s := &sshStore{Nodes: []sshNode{
		{ID: "f1", Type: sshFolder, Name: "folder1"},
		{ID: "a", Type: sshLogin, Parent: "f1", Name: "one"},
		{ID: "b", Type: sshLogin, Parent: "f1", Name: "two", Num: 5},
		{ID: "c", Type: sshLogin, Parent: "f1", Name: "three", Num: 5}, // clash, e.g. after an import
		{ID: "f2", Type: sshFolder, Parent: "f1", Name: "sub", Num: 9},
	}}
	if !s.numberLogins() {
		t.Fatal("expected changes")
	}
	got := map[string]int{}
	for _, n := range s.Nodes {
		got[n.ID] = n.Num
	}
	if got["b"] != 5 || got["a"] != 6 || got["c"] != 7 || got["f1"] != 0 || got["f2"] != 0 {
		t.Fatalf("numbers: %v", got)
	}
	if s.numberLogins() {
		t.Fatal("second pass should change nothing")
	}
	if n := s.findLogin("7"); n == nil || n.ID != "c" {
		t.Fatalf("findLogin(7) = %v", n)
	}
	if n := s.findLogin("#5"); n == nil || n.ID != "b" {
		t.Fatalf("findLogin(#5) = %v", n)
	}
	if got := filterSSHPick([]sshPickItem{{path: "folder1/x7", num: 1}, {path: "folder1/y", num: 7}}, "7"); got[0].item.num != 7 {
		t.Fatalf("number search should rank that login first, got %+v", got)
	}
}
