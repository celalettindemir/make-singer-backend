package model

import "testing"

func TestJob_IsOwnedBy(t *testing.T) {
	j := &Job{OwnerID: "kullanici-a"}
	if !j.IsOwnedBy("kullanici-a") {
		t.Error("sahibi kendi isini gormeli")
	}
	if j.IsOwnedBy("kullanici-b") {
		t.Error("baska kullanici isi gormemeli")
	}
	if j.IsOwnedBy("") {
		t.Error("bos kullanici hicbir isi gormemeli")
	}
	if (&Job{}).IsOwnedBy("") {
		t.Error("sahipsiz kayit bos kullaniciya da acilmamali")
	}
}
