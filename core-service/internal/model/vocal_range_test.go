package model

import (
	"reflect"
	"testing"
)

func TestVocalRangeRecord_CenterErrors(t *testing.T) {
	if got := (VocalRangeRecord{Low: 45, High: 64, ComfortCenter: 44}).CenterErrors(); !reflect.DeepEqual(got, map[string]string{"comfortCenter": "range"}) {
		t.Errorf("outside: %v", got)
	}
	if got := (VocalRangeRecord{Low: 45, High: 64, ComfortCenter: 64}).CenterErrors(); got != nil {
		t.Errorf("edge: %v", got)
	}
}
