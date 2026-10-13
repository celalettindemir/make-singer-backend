package model

import "testing"

func TestJob_IsOwnedBy(t *testing.T) {
	j := &Job{OwnerID: "kullanici-a"}
	if !j.IsOwnedBy("kullanici-a") {
		t.Error("the owner must see their own job")
	}
	if j.IsOwnedBy("kullanici-b") {
		t.Error("another user must not see the job")
	}
	if j.IsOwnedBy("") {
		t.Error("an empty user must not see any job")
	}
	if (&Job{}).IsOwnedBy("") {
		t.Error("an ownerless record must not be open to an empty user either")
	}
}
