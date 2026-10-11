package model

import "testing"

func TestJob_SahibiMi(t *testing.T) {
	j := &Job{OwnerID: "kullanici-a"}
	if !j.SahibiMi("kullanici-a") {
		t.Error("sahibi kendi isini gormeli")
	}
	if j.SahibiMi("kullanici-b") {
		t.Error("baska kullanici isi gormemeli")
	}
	if j.SahibiMi("") {
		t.Error("bos kullanici hicbir isi gormemeli")
	}
	if (&Job{}).SahibiMi("") {
		t.Error("sahipsiz kayit bos kullaniciya da acilmamali")
	}
}
